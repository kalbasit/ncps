## 1. Remove obsolete migration-cutover test

- [x] 1.1 Confirm `dev-scripts/test-migration-e2e.py` has no Taskfile target, CI workflow, nix check, or Python importer (re-grep `test-migration-e2e`, `DBMATE_MIGRATIONS_DIR` repo-wide).
- [x] 1.2 Delete `dev-scripts/test-migration-e2e.py`.
- [x] 1.3 Verify the shared dbmate plumbing is untouched: `dbmate create`/`drop` in `dev-scripts/run.py` (`run_db_migration`, `perform_clean`) and `dev-scripts/migrate-all.py` still present and used by other scenarios.
- [x] 1.4 Grep the repo for any remaining reference to the deleted file (docs, README, comments in `test-cdc-lifecycle-e2e.py` that name it as a successor) and update/remove stale mentions. (Code comments in the CDC driver cleaned; archived changes left immutable; the descriptive `cdc-lifecycle-e2e` spec prose left as historical reference.)

## 2. Contention driver scaffolding

- [x] 2.1 Create `dev-scripts/test-inflight-staging-contention-e2e.py` skeleton (argparse: `--storage {local,s3,both}`, `--window {download,chunking,both}`, `--replicas N` default 2, `--clients N`, `--package`, `--keep-going`), mirroring the structure/colors/logging of `test-cdc-lifecycle-e2e.py`.
- [x] 2.2 Launch the cluster via `run.py --replicas N --locker redis --inflight-staging --storage <s>` (plus `--enable-cdc` when set); wait for all replicas ready on their ports.
- [x] 2.3 Read each replica's `state.json` and assert effective `locker == redis` and `inflight_staging == true`; abort with a clear error otherwise. (Spec: "Multi-replica redis-locker cluster is launched with staging enabled". Verified live.)

## 3. Drive contention and capture results

- [x] 3.1 Reset cache state (run.py `--clean`), realise a large store path locally (`nix build`) for the canonical reference, then race it uncached through ncps.
- [x] 3.2 Implement N concurrent client fetches of the same NAR via a `threading.Barrier` (raw HTTP GETs spread across replicas), capturing each reader's full body.
- [x] 3.3 Detect staging activation via the per-replica debug log line (`STAGING_ACTIVATION_LOG`); fail the run if no waiter ever activated staging (no-op run is not a pass). On non-activation, classify producer-error vs. contended-only vs. no-contention. (Spec: "Concurrent same-NAR fetch activates staging". Activation signal grounded in `pkg/cache/cache.go`.)

## 4. Assertions

- [x] 4.1 Decompress each reader's NAR and compute a content digest; assert all readers' digests are equal AND match the canonical store-path NAR (`nix-store --dump`); fail on any short/truncated/mismatched body even under HTTP 200. (Spec: "All racing readers receive identical complete NARs". Verified live — byte-identity held.)
- [x] 4.2 Run the scenario in the download window (CDC off) and the chunking window (CDC on) as separate phases with independent per-window pass/fail reporting. (Spec: both window scenarios.)
- [x] 4.3 Parameterize over `local` and `s3` backends (`--storage both`); apply identical assertions per backend. (Spec: "Storage-backend matrix".)
- [x] 4.4 Write per-run results (per phase × backend) to `.e2e-results/inflight-staging/`, with a final SUMMARY and non-zero exit on any failure.

## 5. One-command wrapper

- [x] 5.1 Create `dev-scripts/test-inflight-staging-contention-auto.sh` mirroring `test-cdc-lifecycle-auto.sh`: bootstrap direnv, start fixed-port `nix run .#deps` (provides Redis) on a dedicated process-compose port (8512), wait for service readiness, run the driver with pass-through args, tear down on EXIT. (Spec: "Wrapper manages the dependency lifecycle". Verified live.)
- [x] 5.2 Add a `task test:inflight-staging-contention` target that invokes the wrapper, consistent with `test:cdc-lifecycle`.

## 6. Validate and document

- [~] 6.1 Ran the wrapper end-to-end (local/download). HARNESS VALIDATED: cluster launch, state.json checks, the cross-replica race, and byte-identity vs. canonical `nix-store --dump` all PASS, and contention is genuinely reproduced (holder wins the lock + starts the producer; waiters lose it + poll). The activation assertion correctly FAILS-LOUD on a real ncps finding (NOT a harness defect): staging never *serves* because the producer errors `open temp file ... no such file or directory` — on a fast upstream the staging request lands at download completion (`stagingActivationPollInterval = 1s`) and the producer races into the holder's already-removed temp file (`pkg/cache/inflight_staging.go:225`). DECISION (user): keep this change harness-only; the ncps producer fix + happy-path-green validation (and the s3/chunking-window runs, which would hit the same bug) are DEFERRED to a separate change. The in-scope deliverable — a correct contention harness — is complete and verified; see [[project_inflight_staging_producer_temp_race]].
- [x] 6.2 `task fmt` (0 changed), `task lint` (0 issues), `task test` (all pass). Driver `py_compile`s; wrapper `bash -n` clean.
- [x] 6.3 Usage documented via the driver's module docstring (flags, wrapper, deps, opt-in / not in `nix flake check`) + the `test:inflight-staging-contention` task `desc`, matching the self-documented convention of the sibling `test:cdc-lifecycle` driver.
