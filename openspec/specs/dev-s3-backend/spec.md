# Dev S3 Backend Specification

## Purpose

Defines the S3-compatible object storage backend used for local development, `nix flake check`, and Kubernetes integration tests, including the env var contract tests and scripts depend on.
## Requirements
### Requirement: Dev S3 backend SHALL be Garage

Local development and `nix flake check` SHALL use [Garage](https://garagehq.deuxfleurs.fr/) (`pkgs.garage`) as the S3-compatible object store. MinIO (`pkgs.minio`, `pkgs.minio-client`) SHALL NOT be referenced in the dev shell, process-compose services, Nix package `preCheck`, the dev/test helper scripts under `dev-scripts/`, or k8s integration tests. Dev/test scripts that manipulate the S3 backend (e.g. resetting the bucket between runs) SHALL use the S3 SDK already provided by the dev shell (`boto3`), not the MinIO client `mc`.

#### Scenario: Dev shell exposes Garage, not MinIO

- **WHEN** a developer enters the Nix dev shell
- **THEN** `garage --version` SHALL succeed
- **AND** `minio --version` and `mc --version` SHALL NOT be in `PATH`

#### Scenario: `nix run .#deps` starts a Garage server

- **WHEN** a developer runs `nix run .#deps`
- **THEN** a Garage server SHALL be started under process-compose
- **AND** the server SHALL accept S3 v4 signed requests on `127.0.0.1:9000`
- **AND** no MinIO process SHALL be started

#### Scenario: `nix flake check` exercises the S3 backend against Garage

- **WHEN** `nix flake check` runs the ncps package check phase
- **THEN** the `preCheck` phase SHALL start a Garage server
- **AND** integration tests gated by `NCPS_TEST_S3_*` env vars SHALL run against Garage
- **AND** all S3 integration tests SHALL pass

#### Scenario: Dev/test scripts reset the bucket via the S3 SDK, not `mc`

- **WHEN** a dev/test helper script under `dev-scripts/` resets the S3 bucket between runs
- **THEN** it SHALL empty the bucket's objects using `boto3` (the S3 SDK in the dev shell)
- **AND** it SHALL NOT invoke `mc` (the MinIO client) or assume `mc` is on `PATH`
- **AND** it SHALL NOT delete-and-recreate the bucket, because the dev access key is scoped to the pre-provisioned bucket and cannot create buckets

### Requirement: Dev S3 backend SHALL expose a backend-neutral env var contract

The dev/test S3 backend SHALL expose its connection parameters to tests and scripts through env vars prefixed `NCPS_TEST_S3_*`, not implementation-branded names. The `MINIO_*` and `MINIO_TEST_S3_*` env var names SHALL NOT be set or read by project code.

#### Scenario: Process-compose exports neutral env var names

- **WHEN** the dev S3 process-compose service is running
- **THEN** the following env vars SHALL be set in the service environment:
  - `NCPS_TEST_S3_ENDPOINT` (e.g. `http://127.0.0.1:9000`)
  - `NCPS_TEST_S3_REGION` (e.g. `us-east-1`)
  - `NCPS_TEST_S3_BUCKET` (e.g. `test-bucket`)
  - `NCPS_TEST_S3_ACCESS_KEY_ID`
  - `NCPS_TEST_S3_SECRET_ACCESS_KEY`
- **AND** no `MINIO_*` env vars SHALL be set by the service

#### Scenario: `enable-s3-tests` sets neutral names

- **WHEN** a developer runs `eval "$(enable-s3-tests)"`
- **THEN** the shell SHALL have `NCPS_TEST_S3_*` env vars exported
- **AND** the shell SHALL NOT have `MINIO_*` env vars exported by the helper

#### Scenario: Go tests read neutral names

- **WHEN** an S3 integration test reads its connection settings
- **THEN** it SHALL read from `NCPS_TEST_S3_*` env vars
- **AND** it SHALL skip when those env vars are unset

### Requirement: Dev S3 backend SHALL self-validate on startup

The dev S3 backend's init step SHALL create the test bucket and access key, then run a smoke test that exercises the S3 paths ncps depends on. Startup SHALL fail loudly if any step fails.

#### Scenario: Bucket and key bootstrap

- **WHEN** the dev S3 init service runs
- **THEN** it SHALL ensure a bucket named `NCPS_TEST_S3_BUCKET` exists
- **AND** it SHALL ensure an access key with `NCPS_TEST_S3_ACCESS_KEY_ID` / `NCPS_TEST_S3_SECRET_ACCESS_KEY` exists with read/write access to that bucket
- **AND** the operation SHALL be idempotent across restarts

#### Scenario: Smoke test covers put/get/presign

- **WHEN** the init service runs after bucket+key bootstrap
- **THEN** it SHALL upload a known test object using the bootstrapped credentials
- **AND** it SHALL fetch the object via HTTP GET using a presigned URL and verify the contents
- **AND** it SHALL exit non-zero if any step fails

#### Scenario: Anonymous access is blocked

- **WHEN** the init service runs after bucket+key bootstrap
- **THEN** it SHALL verify that anonymous (unsigned) GET against the test object returns a 4xx HTTP status
- **AND** it SHALL exit non-zero if anonymous access succeeds

### Requirement: K8s integration tests SHALL deploy Garage

The Kind-based integration test harness (`nix/k8s-tests/`) SHALL deploy Garage as the in-cluster S3 backend for all S3-backed permutations. No MinIO image, manifest, or chart SHALL be referenced by `k8s-tests`.

#### Scenario: S3-backed permutations boot against Garage

- **WHEN** `k8s-tests install` runs an S3-backed permutation (e.g. `single-s3-postgresql`, `ha-s3-postgres-redis`)
- **THEN** a Garage Deployment SHALL be created in the cluster
- **AND** the ncps pods SHALL be configured with `NCPS_TEST_S3_ENDPOINT` pointing at the in-cluster Garage Service
- **AND** the test SHALL pass end-to-end

#### Scenario: Garage image is pre-loaded into Kind

- **WHEN** `k8s-tests cluster create` provisions the Kind cluster
- **THEN** the Garage container image SHALL be pre-loaded into Kind nodes (matching how MinIO was loaded previously)
- **AND** image pulls SHALL NOT depend on Docker Hub at test time

