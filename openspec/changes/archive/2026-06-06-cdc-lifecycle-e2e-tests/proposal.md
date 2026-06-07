## Why

The CDC (content-defined chunking) lifecycle — `non-CDC → CDC → drain → non-CDC` — is where this session's hardest production bugs lived (phantom NARs, upload-reference 404s, drain mode stuck, stale chunk-store routing). Those behaviors are now spec'd and patched, but nothing exercises the full lifecycle end-to-end. `dev-scripts/test-migration-e2e.py` covers only the dbmate→Ent migration; there is no equivalent for the CDC lifecycle, and the topology-dependent failure modes (drain auto-exit on pod restart, multi-replica shared-DB presence, NFS lag, chunk-store auto-derivation) are invisible to any single-process test.

## What Changes

- Add a **fast local e2e script** (`dev-scripts/test-cdc-lifecycle-e2e.py`), a sibling to `test-migration-e2e.py`, that brings up ncps + backends via the existing `task test:deps` / `nix run .#deps` harness and drives the lifecycle over HTTP + the `ncps` CLI, asserting DB and serving invariants at each phase.
- Add a **new `k8s-tests` lifecycle dimension** (extending `nix/k8s-tests/config.nix`, not a separate framework) that runs the same phases on a Kind cluster to catch topology behaviors a single process cannot.
- Wire both into the existing task/CI surfaces (`Taskfile.yml`, `nix flake check` cohorts) as additive signals.

Phases asserted by each test: CDC-off baseline serve/push → enable CDC (eager + lazy) and verify chunking + narinfo normalization-at-serve → disable CDC and verify drain mode active + `migrate-chunks-to-nar` drains chunks → restart and verify `initCDCDrainMode` auto-completes (stored CDC config cleared, no chunk store). Cross-cutting checks folded in: upload/reference-presence, non-destructive narinfo purge, and fsck repair-not-delete.

## Capabilities

### New Capabilities
- `cdc-lifecycle-e2e`: A fast, local, single-host end-to-end test driving the full non-CDC→CDC→drain→non-CDC lifecycle over HTTP and the `ncps` CLI, asserting DB state and serving invariants at every phase transition plus the cross-cutting checks.
- `cdc-lifecycle-k8s-test`: A new k8s-tests permutation/dimension that runs the lifecycle on a multi-replica Kind cluster to verify topology-only behaviors — drain auto-exit on pod restart, shared-DB presence consistency, storage lag tolerance, and chunk-store auto-derivation.

### Modified Capabilities
<!-- None. This change adds tests that exercise existing behavior specs (cdc-chunking, cdc-disable, cdc-drain-mode, chunks-to-nar-migration, fsck, upload-reference-presence, narinfo-purge-serving); no production requirements change. -->

## Impact

- **New files**: `dev-scripts/test-cdc-lifecycle-e2e.py`; new permutation/feature entries in `nix/k8s-tests/config.nix` (+ template wiring in `src/lib.sh` if new flags are needed).
- **Modified files**: `Taskfile.yml` (new task target), flake-check cohort wiring for CI.
- **No production code changes**: ncps server, CLI, and storage backends are exercised as-is.
- **I/O / network / memory**: No impact on the running service. Test execution is I/O- and network-heavy (NAR build/push/fetch, chunk reassembly during drain) and the k8s dimension adds Kind cluster runtime to CI; scoped to dedicated test cohorts so it does not slow the unit-test path.

## Non-goals

- No changes to CDC, drain, migration, fsck, or purge production behavior — those specs and fixes ship separately.
- Not replacing `dev-scripts/test-migration-e2e.py`; the new script is a sibling covering a different concern.
- Not a general-purpose chaos/perf framework; only the enumerated lifecycle phases and cross-cutting invariants.
