## 1. Replace `mc` with `boto3` in dev-script S3 cleanup

- [x] 1.1 `dev-scripts/run.py`: rewrite the S3 cleanup in `cleanup()` to use `boto3` (endpoint/credentials from `S3_CONFIG`, path-style addressing) and empty the bucket via `list_objects_v2` paginator + `delete_objects` (1000-key batches); drop the `mc rb`/`mc mb` calls and the `shutil.which("mc")` gate.
- [x] 1.2 `dev-scripts/test-migration-e2e.py`: rewrite `reset_everything`'s S3 reset the same way (empty objects via `boto3`), removing the `mc rb`/`mc mb` (the latter ran with `check=True`, hard-failing the run).
- [x] 1.3 Confirm no remaining `mc`/`MC_HOST_*` references in `dev-scripts/`.

## 2. Fix the mysql reserved-word quoting in the CDC driver

- [x] 2.1 `dev-scripts/test-cdc-lifecycle-e2e.py`: add `quote_ident(db, name)` (backticks for `mysql`, double-quotes for sqlite/postgres) and quote `key` in `cdc_config_keys_present`'s `SELECT key FROM config WHERE key IN (...)`.

## 3. Fix the k8s-tests SQLite probe

- [x] 3.1 `nix/k8s-tests/src/k8s_tests_tester.py`: remove `--profile=restricted` from both SQLite `kubectl debug` invocations so the root `nouchka/sqlite3` image is permitted, matching the storage probe (which omits the profile and passes).

## 4. Harden operator install against transient GitHub-CDN 504s

- [x] 4.1 `nix/k8s-tests/src/k8s_tests.py`: add `run_cmd_with_retry` and wrap the four operator `helm upgrade --install` calls (cnpg, mariadb-operator-crds, mariadb-operator, redis-operator). Operator charts are fetched from GitHub release assets whose CDN intermittently returns `504 Gateway Timeout`; a single transient fetch failure previously aborted the entire `cluster create`.

## 5. Validate

- [x] 5.1 `task fmt`, `task lint` (0 issues), `task test` — all exit 0.
- [x] 5.2 Full CDC lifecycle matrix green: sqlite (s3+local), postgres (s3+local), mysql (s3) all PASS via `dev-scripts/test-cdc-lifecycle-e2e.py` against `nix run .#deps`.
- [x] 5.3 `k8s-tests all` reaches **13/13** (was 11/13) — confirmed live: both `single-local-sqlite` and `single-s3-sqlite` now report `✅ Database: SQLite database accessible`, the exact check that previously timed out. The same run exercised the operator-install retry (cnpg's chart 504'd on attempts 1–4, then succeeded → `Cluster created successfully`), validating both fixes.
- [x] 5.4 Update affected spec: `dev-s3-backend` MinIO/`mc` prohibition extended to `dev-scripts/`, plus an SDK-cleanup scenario.
