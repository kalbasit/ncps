## Why

In-flight NAR staging (`inflight-nar-staging`) is the fix for the #660 download-window and #1289 chunking-window "incomplete NAR served under contention" bugs. Yet **no test deliberately activates it.** Staging only engages when a reader *races* an in-flight download; Go tests cover its logic in isolation with mocks, and the HA k8s permutations merely set `inflightStaging.enabled = true` in config (`nix/k8s-tests/config.nix:225,252,277`) without ever driving concurrent same-NAR fetches. The dev harness can now express the scenario — PR #1372 added `run.py --inflight-staging`, and `--locker redis` / `--replicas` already exist — but no driver uses them; every dev e2e driver runs single-instance with the local locker. Separately, `dev-scripts/test-migration-e2e.py` tests the one-time dbmate→Ent cutover, which has shipped; its premise (checkout `main`, migrate forward) is now obsolete.

## What Changes

- **Add a multi-process contention e2e driver** (`dev-scripts/`) that spawns ≥2 `ncps` replicas via `run.py` with `--locker redis --inflight-staging` against shared storage, then issues concurrent fetches of the same large NAR to force staging activation. It asserts every racing reader receives a **complete, byte-identical** NAR (no truncated/incomplete serve), in both the download-window (pre-CDC whole-file) and chunking-window (`--enable-cdc`) modes, across the `local` and `s3` backends.
- **Add a fixed-port `task`/wrapper entry point** for the driver, mirroring `test-cdc-lifecycle-auto.sh` (start `nix run .#deps`, run driver, tear down).
- **Remove** `dev-scripts/test-migration-e2e.py` and its in-file dbmate plumbing (the `DBMATE_MIGRATIONS_DIR` injection and its explanatory comments). The shared `dbmate create`/`drop` calls in `run.py` (`run_db_migration`, `reset_everything`) and the standalone `migrate-all.py` are retained — they provision/reset dev PG/MySQL databases for all scenarios and are not migration-test-specific.

## Capabilities

### New Capabilities
- `inflight-staging-contention-e2e`: An end-to-end driver that activates in-flight NAR staging by racing concurrent readers against an in-flight download across multiple replicas with a distributed (Redis) locker, and asserts complete byte-identical NAR delivery to every reader in both the download and chunking windows.

### Modified Capabilities
<!-- None. The test-migration-e2e.py removal has no governing spec. -->

## Impact

- **Code**: new driver + wrapper under `dev-scripts/`; deletion of `dev-scripts/test-migration-e2e.py`. No production (`pkg/`, `cmd/`) changes.
- **CI**: optional; the driver requires the fixed-port dev stack (`nix run .#deps`) like the existing CDC-lifecycle driver. Not wired into `nix flake check` unless explicitly added later.
- **I/O / latency / memory**: test-time only. The driver builds and fetches real NARs through `ncps`, exercising real S3/Redis I/O; it does not alter any serving path.

## Non-goals

- Pod-death/failover, network-partition, disk-exhaustion, HPA, rolling-upgrade, and PVC-persistence-across-restart scenarios (real-infra k8s gaps; separate follow-up).
- Eviction/LRU/max-size or compression-codec coverage (not expressible via `run.py` today).
- Retiring `dbmate` from the dev shell or `migrate-all.py`.
