## MODIFIED Requirements

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
