# Proposal: Swap MinIO with Garage

## Why

MinIO is effectively dead as a viable open-source dev/test dependency: the project has stripped most features from the community edition and pushed users toward the commercial AIStor offering, breaking long-standing workflows. We use MinIO purely as a local S3-compatible backend for tests, dev shell, and Kubernetes integration tests — we have no business reason to stay on it. [Garage](https://garagehq.deuxfleurs.fr/) is a fully FOSS S3-compatible object store (AGPL, Rust, packaged in nixpkgs) that fits the same role with a lighter footprint and no enterprise rug-pull risk.

## What Changes

- **BREAKING (dev/test only):** Replace the `minio-server` and `minio-init` process-compose services with a `garage-server` and `garage-init` pair under `nix/process-compose/`.
- Replace `pkgs.minio` and `pkgs.minio-client` with `pkgs.garage` (and `awscli2`/`mc`-equivalent tooling as needed) in dev shell and process-compose runtime inputs.
- Rename environment variables exposed by process-compose from the `MINIO_*` namespace to a backend-neutral `NCPS_TEST_S3_*` namespace (e.g. `NCPS_TEST_S3_ENDPOINT`, `NCPS_TEST_S3_ACCESS_KEY_ID`, `NCPS_TEST_S3_BUCKET`). Update `enable-s3-tests` helper and `nix/packages/ncps/default.nix` `preCheck` to use the new names.
- Update `nix/k8s-tests/` config and helpers to deploy Garage instead of MinIO inside the Kind cluster for S3-backed scenarios.
- Update `dev-scripts/run.sh s3`, `CLAUDE.md`, and any other docs/README references from MinIO to Garage (endpoint, credentials, port mapping, console URL if any).
- Keep the S3 bucket name, region, and test credentials stable where possible to minimize churn in test code.

## Non-goals

- No changes to the application's S3 client code (`pkg/storage/s3/`). Garage is S3-compatible; the production code is backend-agnostic.
- No changes to the runtime S3 client behavior. Users running ncps against AWS S3, Ceph, real MinIO, etc. continue to work — the S3 protocol surface is unchanged.
- No migration tooling for existing dev caches — dev storage is ephemeral.
- No changes to other dev dependencies (Postgres, MariaDB, Redis).
- **In-scope clarification:** all MinIO references in documentation, chart example values, and example configs are replaced with Garage. The project no longer treats MinIO as an example/recommended S3 backend in its own materials.
- **Excluded:** the `github.com/minio/minio-go/v7` Go module import stays. It is a generic, widely-used S3 v4 client library — the "minio" in the name refers to the upstream maintainer, not to a MinIO server dependency. Replacing it would be a multi-file refactor (or full migration to `aws-sdk-go-v2`) with no functional benefit. Go-code comments and the `ExampleMinIO()` doc example are updated to be backend-neutral, but the package identifier `minio.X` from the import remains.

## Capabilities

- **dev-s3-backend** (new): Defines which S3-compatible server backs local development and test workflows, the env var contract it exposes to tests, and the dev-shell helpers that wire it up. No changes to application/production storage capabilities — the runtime S3 client code is backend-agnostic.

## Impact

- **Affected code:** `nix/process-compose/flake-module.nix`, `nix/process-compose/start-minio.sh`, `nix/process-compose/init-minio.sh` (renamed/replaced), `nix/k8s-tests/` config + helpers, `nix/packages/ncps/default.nix` (preCheck env), `dev-scripts/run.sh`, `flake.nix`/`nix/dev-packages.nix` (package swap), `CLAUDE.md`, any `README` mentions.
- **Dependencies:** drop `pkgs.minio`, `pkgs.minio-client`; add `pkgs.garage` (already in nixpkgs).
- **Tests:** integration tests using `enable-s3-tests` continue to pass against Garage. CI (`nix flake check`) and `k8s-tests` workflows must remain green.
- **I/O / network / memory:** Garage uses less RAM than MinIO at idle (~50MB vs ~200MB) and listens on a single S3 endpoint (no separate console). No change to test latency expectations.
- **Container image:** dev base image (`docker-dev`) needs `update-cu-base` re-run after package swap.
- **Developer workflow:** anyone with `MINIO_*` env vars in their shell or scripts must switch to the new `NCPS_TEST_S3_*` names; `enable-s3-tests` handles this automatically inside the dev shell.
