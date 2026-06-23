## Why

The `e2e-nightly` kubernetes leg has failed every scheduled run since 2026-06-11 on the three CDC scenarios (`single-s3-postgres-cdc`, `ha-s3-postgres-cdc`, `ha-s3-postgres-cdc-lifecycle`) with `PostgreSQL database is empty (0 chunks)`. The kubernetes harness isolates a database per scenario but shares one Garage S3 bucket (`ncps-bucket`) across every scenario and never cleans it. Earlier non-CDC scenarios reuse the same test narinfo hashes and leave whole-file `.nar.xz` objects in `store/nar/`; when a CDC scenario then runs with a fresh database against that dirty bucket, ncps (since PR #1393 treats a stored `.nar.xz` as a present `Compression:none` NAR) sees the residual whole-file as already present, skips the upstream download + eager chunking, and creates zero chunk rows — failing the validation. The storage substrate must be isolated per scenario exactly as the database already is.

## What Changes

- The kubernetes e2e harness (`nix/e2e-tests/src/k8s_tests.py`) SHALL give each scenario its own S3 bucket instead of a single shared `ncps-bucket`, mirroring the existing per-scenario database isolation.
- Per-scenario buckets SHALL be created and granted to the access key during cluster/garage setup (idempotently), derived deterministically from the scenario name.
- Each scenario's generated Helm values SHALL reference its own bucket so it deploys against clean storage.
- This removes the residual-data cross-contamination and also closes a latent false-positive: the S3 storage validation check (counting `store/chunk/`) currently can pass on residual chunks from a previous scenario.
- Scope is the e2e **test harness only**; no ncps production code changes.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `unified-e2e-harness`: the harness's per-scenario isolation requirement is extended so the S3 storage backend (bucket) is isolated per scenario in kubernetes mode, not just the database.

## Impact

- `nix/e2e-tests/src/k8s_tests.py` — garage bucket setup (create/grant per scenario), per-scenario Helm value generation (bucket name), and the S3 validation check operate against the per-scenario bucket.
- Possibly `nix/e2e-tests/config.nix` if the bucket name is sourced from the shared S3 config used in value generation.
- Restores the `e2e-nightly` kubernetes leg to green; no production code, Helm chart defaults, or ncps behavior change.
