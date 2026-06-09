## Context

In-flight NAR staging (`inflight-nar-staging`) exists to stop `ncps` from serving truncated/incomplete NARs to a reader that races an in-flight download — the #660 download-window bug — and its CDC-window analogue, #1289. The feature only engages **under contention**: a second reader arriving while a NAR is still being fetched becomes a *waiter*, which activates staging so the waiter is fed from committed part-objects rather than a half-written stream. It requires a distributed locker (`--locker redis`); on the local locker it self-disables.

Current test reality (from the coverage audit):
- **Go tests** (`pkg/cache/inflight_staging_*_test.go`, 6 files / 19 funcs) cover activation, the state machine, producer, reader, GC, takeover, and precedence — but with in-process fakes, single process.
- **k8s-tests** HA permutations (`nix/k8s-tests/config.nix:225,252,277`) set `inflightStaging.enabled = true` but never drive concurrent same-NAR fetches, so staging never *activates* during the run.
- **dev-scripts** drivers (`test-cdc-lifecycle-e2e.py`) are all single-instance, local-locker — they cannot reach the feature at all.

PR #1372 added `run.py --inflight-staging`; `--locker redis` and `--replicas N` already exist (with a guard requiring redis when `replicas > 1`). The pieces to drive a real multi-process contention test now exist, but nothing uses them. Separately, `test-migration-e2e.py` validates the dbmate→Ent cutover, which has shipped and is no longer reachable from current `main` in a meaningful way.

## Goals / Non-Goals

**Goals:**
- A dev-harness e2e driver that *actually activates* in-flight staging by racing concurrent readers against an in-flight NAR download across ≥2 replicas with the redis locker, and asserts every reader gets a complete, byte-identical NAR.
- Cover both windows the feature protects: the **download window** (pre-CDC whole-file) and the **chunking window** (`--enable-cdc`), on both `local` (shared path) and `s3` storage.
- A fixed-port wrapper mirroring `test-cdc-lifecycle-auto.sh` so the driver is one command.
- Remove the obsolete `test-migration-e2e.py` and its in-file dbmate plumbing without disturbing shared dbmate usage.

**Non-Goals:**
- Real-infra failure injection (pod kill, network partition, disk exhaustion, lock-TTL expiry), HPA, rolling upgrade, PVC-persistence-across-restart — these are k8s/chaos follow-ups.
- Wiring the driver into `nix flake check` / CI gating (it needs the fixed-port stack; keep it opt-in like the CDC driver).
- Any change to production serving paths, or to `dbmate`/`migrate-all.py`.

## Decisions

**D1 — Reuse `run.py` multi-replica mode rather than a bespoke launcher.**
`run.py --replicas N --locker redis --inflight-staging` already spawns N instances behind the existing guard. The driver shells out to it exactly as `test-cdc-lifecycle-e2e.py` does, reading `state.json` (which now records `inflight_staging`) to confirm the effective config. *Alternative considered:* a new Go integration test harness — rejected: the bug is inherently multi-process + real redis + real storage; in-process tests already exist and cannot reproduce the cross-replica race.

**D2 — Force contention deterministically via a slow/large NAR, not timing luck.**
Activation needs a waiter to arrive *during* a download. The driver seeds a large package (long closure / big NAR) and issues N concurrent client fetches of the same store path (e.g. parallel `nix copy --from`/HTTP GETs of the same `.narinfo`+NAR) immediately after cache reset, so the first triggers the upstream pull and the rest become waiters. *Alternative considered:* relying on natural build concurrency — rejected as non-deterministic and flaky.

**D3 — Assert on byte-identity of the served NAR, not just HTTP 200.**
The #660/#1289 failures returned a 200 with a truncated/incomplete body. Each concurrent reader's received NAR must be captured and compared (decompressed-content digest) against a single source of truth, and against each other. A passing run requires *all* readers identical and complete. This mirrors the byte-identity checks already in `test-cdc-lifecycle-e2e.py`.

**D4 — Two windows as explicit phases.** Phase A: staging disabled-window baseline / download-window with whole-file NARs (CDC off). Phase B: chunking-window (`--enable-cdc`). Both with staging on. This isolates which window a regression lives in.

**D5 — Storage matrix `local` + `s3`; locker fixed to `redis`.** Local storage in multi-replica mode uses a shared `var/ncps` path (the documented shared-RWX topology); S3 is the natural distributed backend. The local locker is out of scope because the feature is a no-op there.

**D6 — Removal scope is the file + its in-file dbmate plumbing only.** Delete `dev-scripts/test-migration-e2e.py` (no Taskfile/CI/nix wiring, no importers). The `DBMATE_MIGRATIONS_DIR` injection lives only inside that file and dies with it. Keep `run.py`'s `dbmate create/drop` and `migrate-all.py` — they provision/reset dev PG/MySQL for every scenario.

## Risks / Trade-offs

- **[Flaky activation — the race may not reliably produce a waiter]** → Make the download window wide (large NAR, optional upstream-latency knob) and have the driver verify via `state.json` / metrics that staging actually *activated* (a waiter was registered); fail loudly if it didn't, rather than passing a no-op run.
- **[Local multi-replica on a shared path may surface unrelated NFS/RWX races]** → Use a single local filesystem path (not networked) for the dev run; the topology realism is the S3 phase's job. Document that local-phase failures unrelated to staging should be triaged before blaming the feature.
- **[Slow / heavyweight driver]** → Keep it opt-in (wrapper + optional `task` target), not in `nix flake check`; reuse the fixed-port `nix run .#deps` stack already required by the CDC driver.
- **[Redis required]** → The wrapper starts the full fixed-port stack (which includes Redis); the driver errors clearly if `--locker redis` is not effective.

## Migration Plan

1. Land the driver + wrapper (additive; no production impact).
2. Remove `test-migration-e2e.py` in the same change (independent of the driver). Rollback is a pure git revert; nothing depends on it.
3. No deployment, schema, or runtime change — test-tooling only.

## Open Questions

- **Client mechanism for concurrent fetch**: reuse `nix-isolated-build.py` against the running cache, or issue raw concurrent HTTP GETs of a known store path? Raw HTTP gives tighter control over timing and byte-capture; an isolated `nix copy` is more realistic. Lean HTTP for determinism, decided at implementation.
- **Activation observability**: assert via `state.json`, a staging metric (`ncps_inflight_staging_*`), or a log scrape? Prefer a metric/state signal over log scraping if one is exported.
