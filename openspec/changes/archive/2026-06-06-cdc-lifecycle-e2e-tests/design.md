## Context

ncps supports content-defined chunking (CDC): NARs can be stored as whole files or as chunk sequences, and CDC can be toggled at runtime. Disabling CDC while chunked NARs remain puts the service into *drain mode* — it keeps a chunk store alive to serve existing chunked NARs until `ncps migrate-chunks-to-nar` rewrites them as whole files; on the next boot `initCDCDrainMode` (`pkg/ncps/serve.go`) detects zero remaining chunked NARs, clears the stored CDC config (`pkg/config/config.go` `DeleteCDCConfig`), and starts without a chunk store.

This lifecycle is the source of this session's hardest production incidents (phantom NARs, `nix copy .../upload` aborts, drain stuck, stale chunk-store routing). The individual behaviors are now spec'd and patched, but nothing exercises the lifecycle end-to-end. `dev-scripts/test-migration-e2e.py` is the closest existing pattern but covers only the dbmate→Ent migration. The topology-dependent failure modes (drain auto-exit on pod restart, multi-replica shared-DB presence, storage lag, chunk-store auto-derivation) are structurally invisible to any single-process test.

Existing assets to reuse: the `task test:deps` / `nix run .#deps` process-compose harness (Garage, Postgres, MariaDB, Redis), `dev-scripts/run.py` + `nix-isolated-build.py` for driving ncps and seeding cache, and the `nix/k8s-tests` Kind harness with its declarative `config.nix` permutation matrix.

## Goals / Non-Goals

**Goals:**
- A fast, local, single-host e2e driver (`dev-scripts/test-cdc-lifecycle-e2e.py`) that asserts DB + serving invariants at every lifecycle phase transition, wired to a `task` target for cheap CI logic signal.
- A new `k8s-tests` permutation/dimension that runs the same phases on a multi-replica Kind cluster to catch topology-only behaviors.
- Reuse of existing harnesses and patterns; both tests drive ncps only through HTTP and the `ncps` CLI.

**Non-Goals:**
- No production code changes to CDC, drain, migration, fsck, or purge — those ship as separate changes.
- Not replacing `test-migration-e2e.py`.
- No chaos/perf/fuzz framework; only the enumerated phases and cross-cutting invariants.

## Decisions

**1. Python driver mirroring `test-migration-e2e.py` (vs. a Go integration test).**
The lifecycle requires orchestrating an out-of-process ncps server, runtime config flips, CLI subcommands (`migrate-chunks-to-nar`, `fsck`), and process restarts — exactly the shape `test-migration-e2e.py` already solves with `run.py` + readiness probing + DB snapshots. A Go `_test.go` would have to re-implement process lifecycle and restart-to-trigger-`initCDCDrainMode`. Reusing the Python pattern keeps it consistent and is the literal "successor" the proposal asks for. Go-level CDC behavior already has unit/integration coverage (`pkg/cache/cdc_test.go`, `pkg/ncps/migrate_chunks_to_nar_test.go`); this fills the *orchestration* gap.

**2. Phase assertions read DB state directly + serve over HTTP.**
Each phase asserts both the serving invariant (fetch back, compare bytes / presence) and the DB invariant (chunk counts, presence of CDC config keys), following the `snapshot_db()` approach in the existing script. Direct DB reads are acceptable in a test driver and catch phantom-record bugs that HTTP alone misses.

**3. Restart is the trigger for `initCDCDrainMode` assertions.**
Drain auto-completion only happens on boot. The driver stops and restarts the ncps process (no `--clean`) after `migrate-chunks-to-nar`, then asserts stored CDC config is gone and no chunk store was created (via boot logs and/or DB config table).

**4. k8s test as a new `config.nix` permutation, extending the matrix.**
Per the proposal, this is a new dimension in the existing harness, not a new framework. Start from the existing `ha-s3-postgres-cdc` permutation (2 replicas, CDC on) and add lifecycle phase scripting. Topology assertions (cross-replica presence, drain auto-exit on pod delete) live in the k8s test body, not the local driver. Alternative considered: a bespoke harness — rejected as a tangent.

**5. CI placement in dedicated cohorts.**
Local driver runs in an integration cohort behind `nix flake check`; the k8s permutation runs in the existing Kind cohort. Kept off the unit-test path to avoid slowing it. (See memory: CI slowness from re-running full integration suites — scope to the right cohort/system.)

## Risks / Trade-offs

- [Flakiness from real NAR builds / network] → Seed a small fixed package set (as `test-migration-e2e.py` does), prefer already-cached fixtures, and use generous readiness probes.
- [k8s test runtime cost in CI] → Single dedicated permutation reusing the existing Kind harness; gate to the Kind cohort, not every PR leg.
- [Storage-lag assertions are inherently timing-sensitive] → Assert the *invariant* (HEAD never 200s with absent bytes) rather than a fixed timing window; retry reads with bounded backoff.
- [Drift between local and k8s phase definitions] → Keep the canonical phase list in one place (shared shell/lib or a documented sequence) so both tests assert the same transitions.
- [openspec-guard blocks merge on active changes] → This change must be archived before merge (see memory `project_openspec_guard_requires_archive`).

## Migration Plan

Purely additive test infrastructure; no runtime/data migration and nothing to roll back. Rollout: (1) land the local driver + `task` target, (2) add the k8s permutation to `config.nix` and regenerate, (3) wire both into the appropriate CI cohorts. Each step is independently revertible by removing the added files/entries.

## Open Questions

- Eager vs. lazy chunking: assert both in one run (toggle mid-run) or split into two driver invocations? Leaning toward one run that covers both paths.
- Should the k8s permutation use S3 (matching existing CDC permutations) or also cover the local-on-shared-storage topology that triggered the original NFS-lag incidents? Local-on-RWX is the higher-signal topology but costs more to set up in Kind.
- Exact mechanism to assert "no chunk store after restart" in k8s — boot log scrape vs. an introspection endpoint vs. DB config-table check.
