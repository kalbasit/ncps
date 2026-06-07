## 1. Local e2e driver scaffold

- [x] 1.1 Create `dev-scripts/test-cdc-lifecycle-e2e.py` skeleton modeled on `test-migration-e2e.py` (arg parsing, results dir, structured logging)
- [x] 1.2 Reuse the dependency harness to discover backend URLs (SQLite/Postgres/MariaDB, Garage/S3, Redis). NOTE: driver uses the FIXED-port `nix run .#deps` stack (not the random-port `test:deps:start`), because `run.py` is hardwired to fixed dev ports.
- [x] 1.3 Implement ncps lifecycle helpers: start via `run.py`, `/nix-cache-info` readiness probe, clean stop, and restart-without-clean
- [x] 1.4 Implement `snapshot_db()`-style helpers: chunked-nar count (`total_chunks > 0`), presence of CDC config keys (`cdc_*`), total nar_file count
- [x] 1.5 Implement seed/push + fetch helpers over HTTP and `nix-isolated-build.py`; serve-size matches narinfo NarSize
- [x] 1.6 Implement CDC config flip helpers (enable/disable, eager vs lazy via run.py flags) and `ncps` CLI wrappers for `migrate-chunks-to-nar` and `fsck`
- [x] 1.7 Add cleanup/teardown that stops the ncps process on success and failure and preserves logs/artifacts

## 2. Local e2e phase assertions

- [x] 2.1 CDC-off baseline: push + serve a NAR, assert whole-file (zero chunks) in DB and no `cdc_*` keys
- [x] 2.2 CDC-on eager: push a new path, assert chunked-nar count grows and served size matches narinfo
- [x] 2.3 CDC-on lazy: re-read the baseline whole-file NAR under lazy CDC, assert identical length + serve
- [x] 2.4 Narinfo normalization: assert narinfo URL/Compression/NarHash/NarSize present + size-consistent at serve
- [x] 2.5 Drain active: disable CDC with chunks remaining, assert serve-from-chunks and `migrate-chunks-to-nar --dry-run` does not mutate
- [x] 2.6 Drain complete: run `migrate-chunks-to-nar`, assert zero chunked NARs remain
- [x] 2.7 Restart auto-completion: restart ncps, assert `cdc_*` config keys cleared and boot log shows drain completion

## 3. Local e2e cross-cutting invariants

- [x] 3.1 Upload reference presence: assert HEAD/GET agree (both 200, bytes present) for every served narinfo's NAR
- [x] 3.2 RESOLVED (covered by unit tests, not duplicable in e2e): the shared-NAR refcount invariant is already asserted by `pkg/cache/nondestructive_narinfo_purge_internal_test.go::TestPurgeNarInfo_SharedNarFileSurvives` (+ `OrphanedNarFileDeleted`, `OrphanedNoneNarReclaimsZstdBytes`), using a synthetic `seedSharedNar` fixture. Real nix packages essentially never share a NAR, so the e2e driver cannot deterministically construct the case; duplicating it there would violate the no-duplicate-tests guidance. Spec scenario removed from the cdc-lifecycle-e2e capability accordingly.
- [x] 3.3 fsck no-data-loss: assert `ncps fsck --repair` preserves serving of every still-referenced NAR (orphan reclamation, i.e. a row-count decrease, is expected and allowed — the live e2e run showed 40→9 from CDC-toggle orphans). The precise "repair a broken link not delete" case is unit-tested in fsck_test.go.

## 4. Local e2e wiring

- [x] 4.1 Add `task test:cdc-lifecycle` + `dev-scripts/test-cdc-lifecycle-auto.sh` that start fixed-port deps, run the driver, and tear down
- [x] 4.2 RESOLVED (design finding): the driver is a dev/manual e2e tool, NOT a flake-check derivation. Like its sibling `test-migration-e2e.py` (also absent from CI), it builds real nixpkgs packages through ncps over the network, which a sandboxed nix-build cannot do. CI lifecycle coverage comes from the k8s permutation (5.x) + existing Go integration cohorts; the driver runs via `task test:cdc-lifecycle`.
- [x] 4.3 Ran end-to-end locally (sqlite+local) — FULL LIFECYCLE PASS after the CDC serve fix (fix-cdc-incomplete-nar-serve, now in this stack's base): Phases 0-4 + upload-presence + fsck-no-data-loss all green, deterministic. (Pre-fix this run surfaced the serving bug; see archived fix change.)
## 5. k8s-tests lifecycle permutation

- [x] 5.1 Added `ha-s3-postgres-cdc-lifecycle` permutation to `nix/k8s-tests/config.nix` (clone of `ha-s3-postgres-cdc` + `cdc-lifecycle` marker feature); validated via `nix eval` (13 perms, marker present)
- [x] 5.2 Propagated the `cdc_lifecycle` flag from features into the tester config (`k8s_tests.py::_generate_test_config`); registered `cdc-lifecycle` marker in both feature maps. NOTE: harness is Python (`k8s_tests*.py`), not `src/lib.sh` as the task assumed.
- [x] 5.3 Implemented `_test_cdc_lifecycle` phase script in `k8s_tests_tester.py` (Phase A chunking-active → B CDC-disable-via-configmap + restart drain-serving → C `migrate-chunks-to-nar` via in-pod exec reusing `--config` → D restart clears `cdc_enabled`); wired into `test_deployment`. Compiles clean. NEEDS LIVE VALIDATION in 5.4 (cluster not available here).
- [x] 5.4 VALIDATED in-cluster on Kind (with the docker fix in base): `generate --push` → `install` → `test ha-s3-postgres-cdc-lifecycle` = **7/7 checks PASS**, including the lifecycle + topology. (Also fixed a pre-existing harness bug in `_test_s3_storage`: chunk path is `store/chunk/` (singular, per pkg/storage/chunk/s3.go), not `store/chunks/`.)
## 6. k8s-tests topology assertions

- [x] 6.1 VALIDATED in-cluster: `_test_cdc_lifecycle` Phase D (restart) asserts `cdc_enabled` cleared after drain — passed on the 2-replica Kind deployment.
- [x] 6.2 VALIDATED in-cluster: `_test_cdc_topology` asserts all replicas agree on NAR presence — passed across 2 replicas (CDC Topology check green).
- [x] 6.3 VALIDATED in-cluster: `_test_cdc_topology`/`_pod_presence_ok` asserts HEAD never 200s with absent bytes (HEAD==GET, bounded-backoff for lag) — passed across 2 replicas.
- [x] 6.4 VALIDATED in-cluster: chunk-store derivation exercised — Phase A asserts chunking active (4 chunks in S3 `store/chunk/` + DB), Phase D asserts cleared after drain. Passed.
## 7. CI integration and finalization

- [x] 7.1 No wiring needed: `config.nix` is the single source of permutations (flake-module does not enumerate them) and `k8s-tests` is a manual tool not invoked by any `.github/workflows` or `nix flake check` leg, so the new permutation is auto-included whenever `k8s-tests` runs. There is no separate CI Kind cohort to add it to.
- [x] 7.2 Updated `nix/k8s-tests/README.md`: new CDC Lifecycle section for `ha-s3-postgres-cdc-lifecycle`, local-driver counterpart note, and bumped permutation counts 12→13
- [x] 7.3 `task fmt` (0 changed), `task lint` (0 issues), `task test` (full unit suite green incl. the new pkg/cache regression test) all exit zero.
- [x] 7.4 Archived. All tasks complete: local e2e validated (full lifecycle PASS), k8s permutation validated in-cluster (7/7), capabilities synced to openspec/specs.
