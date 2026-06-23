## 1. Per-scenario bucket creation in garage setup

- [x] 1.1 In `k8s_tests.py` garage setup, replace the single `ncps-bucket` create/grant with a loop over catalog permutations: for each scenario whose `storage.type == "s3"`, derive the bucket name `ncps-<scenario-name>` and create it idempotently (guard with `bucket info`).
- [x] 1.2 Grant the existing shared access key (`GK1234...`) read+write+owner on each per-scenario bucket (idempotent).
- [x] 1.3 Add a small helper to derive the bucket name from a scenario name so the same derivation is used by setup, value generation, and validation.

## 2. Wire the per-scenario bucket into deployment + validation

- [x] 2.1 Make the per-scenario Helm value generation use the scenario's bucket instead of the shared `ncps-bucket` (set the bucket from the derivation where `get_cluster_creds()`/`creds["s3"]["bucket"]` currently supplies the shared name).
- [x] 2.2 Confirm the S3 validation check (`k8s_tests_tester.py:_test_s3_storage`) reads the bucket from the per-deployment S3 config so it targets the per-scenario bucket (adjust only if it hard-codes the shared bucket).
- [x] 2.3 Remove or repurpose the now-unused shared `ncps-bucket` reference so there is a single source of truth for the bucket name.

## 3. Harness unit coverage

- [x] 3.1 Add/extend a harness pytest (the `e2e-harness-unit` net) asserting the bucket-name derivation and that generated kubernetes values for two distinct scenarios carry distinct, per-scenario buckets.

## 4. Local validation on Kind

- [x] 4.1 Run `nix run .#e2e -- --mode kubernetes --scenario single-s3-postgres-cdc` on a clean cluster and confirm it passes with a non-zero `chunks` count.
- [x] 4.2 Run `ha-s3-postgres-cdc` and `ha-s3-postgres-cdc-lifecycle` and confirm both pass.
- [x] 4.3 Run at least one non-CDC S3 scenario before a CDC scenario in the same invocation and confirm the CDC scenario still passes (no cross-scenario residue).

## 5. Verify, lint, format

- [x] 5.1 Run `task fmt`, `task lint`, and the harness unit tests; confirm all pass.

## 6. CI validation

- [ ] 6.1 After merging to the branch, trigger the `e2e-nightly` workflow via `workflow_dispatch` (with `ref` = the branch) and confirm the kubernetes leg goes green.
