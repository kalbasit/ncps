## Why

A pre-`v0.10.0` test sweep surfaced three dev/test-harness bugs (the ncps
product itself was healthy in every suite — these are tooling defects):

1. **`dev-scripts/run.py --clean` bricks the Garage bucket.** It deleted the
   bucket with `mc rb` then tried to recreate it with `mc mb`. The dev Garage
   access key is scoped to the pre-provisioned `test-bucket` and lacks global
   `createBucket` permission, so `mc mb` fails (`Forbidden`) — and the error is
   swallowed (`check=False`, stderr→`/dev/null`). After the first `--clean` the
   bucket is gone for the rest of the session, so every subsequent S3-backed run
   fails with `bucket not found` / `ncps did not become ready`. The same
   delete-then-recreate pattern exists in `dev-scripts/test-migration-e2e.py`.

2. **`mc` is not provided by the flake.** It only works because it happens to be
   on a developer's global `PATH`. The `dev-s3-backend` spec already forbids
   MinIO/`mc` in the dev shell, process-compose, `preCheck`, and k8s tests — but
   never enumerated the `dev-scripts/` helpers, which is exactly the gap these
   scripts exploited. MinIO tooling is being dropped from the ecosystem.

3. **The CDC lifecycle e2e driver fails on MariaDB.** `cdc_config_keys_present`
   issues `SELECT key FROM config WHERE key IN (...)` with `key` unquoted. `key`
   is a reserved word in MySQL/MariaDB, so the probe throws error 1064 and the
   `mysql` lifecycle never runs (it works on sqlite/postgres).

4. **The k8s-tests SQLite database probe times out (2 of 13 permutations fail).**
   `nix/k8s-tests/src/k8s_tests_tester.py` introspects the SQLite DB with
   `kubectl debug --image=nouchka/sqlite3:latest --profile=restricted`. The
   `restricted` profile sets `runAsNonRoot` on the ephemeral container, but the
   `nouchka/sqlite3` image runs as root, so the kubelet rejects it
   (`CreateContainerConfigError: container has runAsNonRoot and image will run as
   root`); the debug container never starts and the 60s probe times out. ncps +
   SQLite is healthy (HTTP narinfo serving reads the DB; the storage probe — which
   uses the same `kubectl debug` *without* `--profile=restricted` — passes).

5. **`k8s-tests cluster create` aborts on a single transient operator-chart 504.**
   The four operator charts (cnpg, mariadb-operator ×2, redis-operator) are
   `helm`-installed from GitHub release assets with no retry. GitHub's
   release-asset CDN intermittently returns `504 Gateway Timeout`, so any one
   transient failure aborts the entire cluster bring-up before ncps is deployed.

## What Changes

- `dev-scripts/run.py`: replace the `mc`-based S3 cleanup with `boto3` (a
  declared dev-shell dependency, already used by `nix/k8s-tests`). Empty the
  bucket's objects via `list_objects_v2` + `delete_objects` instead of
  deleting+recreating it, so no `createBucket` permission is needed.
- `dev-scripts/test-migration-e2e.py`: same `mc` → `boto3` empty-the-bucket
  change in `reset_everything`.
- `dev-scripts/test-cdc-lifecycle-e2e.py`: add a dialect-aware `quote_ident`
  helper and quote `key` (backticks for mysql, double-quotes for sqlite/postgres)
  in `cdc_config_keys_present`.
- `nix/k8s-tests/src/k8s_tests_tester.py`: drop `--profile=restricted` from the
  two SQLite `kubectl debug` invocations so the root sqlite image is permitted,
  matching the working storage probe.
- `nix/k8s-tests/src/k8s_tests.py`: add `run_cmd_with_retry` and wrap the four
  operator `helm upgrade --install` calls. The operator charts are fetched from
  GitHub release assets whose CDN intermittently returns `504 Gateway Timeout`;
  a single transient failure previously aborted the whole `cluster create`
  before any ncps pod was deployed.
- No **BREAKING** changes. No production (`pkg/`, `cmd/`, `ent/`) code changes.

## Capabilities

- **New Capabilities**: none.
- **Modified Capabilities**:
  - `dev-s3-backend` — extends the "Dev S3 backend SHALL be Garage" requirement's
    MinIO/`mc` prohibition to explicitly include the `dev-scripts/` helpers, and
    adds a scenario requiring dev/test scripts to manage the bucket via the S3
    SDK (`boto3`) by emptying objects (not delete-and-recreate).

## Impact

- **Code**: `dev-scripts/run.py`, `dev-scripts/test-migration-e2e.py`,
  `dev-scripts/test-cdc-lifecycle-e2e.py`, `nix/k8s-tests/src/k8s_tests_tester.py`.
- **Behavior**: S3-backed CDC/migration e2e runs no longer self-corrupt the
  bucket; the `mysql` CDC lifecycle runs to completion; `k8s-tests all` reaches
  13/13. No change to ncps runtime behavior.
- **Dependencies**: removes the undeclared global `mc` dependency from
  `dev-scripts/`; relies only on `boto3`, already in the dev shell.

## Non-goals

- Pre-loading the `nouchka/sqlite3` / `busybox` debug images into Kind to avoid
  Docker Hub at test time (a separate, pre-existing concern; the image pull is
  not the failure here — the security-policy rejection is).
- Granting the dev Garage key `createBucket` permission (emptying objects is the
  simpler, sufficient fix).
- Any change to ncps product code.
