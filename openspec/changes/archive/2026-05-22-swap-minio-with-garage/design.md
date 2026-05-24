# Design: Swap MinIO with Garage

## Context

ncps uses MinIO as its local S3-compatible backend in three places:

1. **process-compose dev deps** (`nix/process-compose/flake-module.nix`, `start-minio.sh`, `init-minio.sh`) — started via `nix run .#deps`, providing an S3 endpoint on `127.0.0.1:9000` plus a web console on `:9001`.
2. **Nix package check phase** (`nix/packages/ncps/default.nix`) — boots MinIO in `preCheck` so `nix flake check` exercises the S3 backend.
3. **Kind integration tests** (`nix/k8s-tests/`) — deploys MinIO into the Kind cluster for `single-s3-*`, `external-secrets-*`, and HA test permutations.

Integration tests gate on `NCPS_TEST_S3_ENDPOINT` / `NCPS_TEST_S3_ACCESS_KEY_ID` / `NCPS_TEST_S3_BUCKET` / `NCPS_TEST_S3_SECRET_ACCESS_KEY` (set up today via `MINIO_TEST_S3_*`-prefixed vars passed through `enable-s3-tests`).

MinIO upstream has gutted the FOSS community edition, removing the admin/console UI and many features in favor of their commercial AIStor product. We need a stable, fully-FOSS, S3-compatible replacement we can keep in nixpkgs without surprises. [Garage](https://garagehq.deuxfleurs.fr/) (AGPL, Rust, packaged as `pkgs.garage`) is mature, lightweight, and explicitly designed as a drop-in S3 backend for self-hosted setups.

## Goals / Non-Goals

**Goals:**

- Replace MinIO with Garage as the single S3 backend across dev shell, `nix flake check`, and k8s integration tests.
- Move from MinIO-branded env var names (`MINIO_TEST_S3_*`) to backend-neutral names (`NCPS_TEST_S3_*`) so future swaps don't require renames again.
- Preserve the existing S3 contract used by tests: bucket name `test-bucket`, region `us-east-1`, access/secret credentials, endpoint port `9000`.
- Keep `nix flake check`, k8s integration tests, and `dev-scripts/run.sh s3` green after the swap.

**Non-Goals:**

- No changes to `pkg/storage/s3/` or any other production code path.
- No changes to user-facing deployment docs about which S3 backend to run in production.
- No Garage cluster/replication setup — single-node, single-zone, ephemeral storage only (matches today's MinIO single-node config).
- No console/UI replacement. Garage has no web console; the MinIO console at `:9001` goes away.
- No support for keeping MinIO as an alternative dev backend behind a flag.

## Decisions

### D1: Use Garage in single-node "layout" mode

Garage requires an initial cluster layout (zone, capacity) even for single-node. We will script `garage layout assign` + `garage layout apply` in `start-garage.sh` and gate the init script on `garage status` reporting the node as healthy.

**Alternatives considered:**

- *SeaweedFS* — S3 implementation is less complete (no presigned URL parity guarantees, weaker bucket policy support).
- *LocalStack S3* — bigger footprint, AWS-license restrictions on some features, slower startup.
- *fake-s3 / s3mock* — not maintained / not S3 v4 signature compliant.

### D2: Use Garage's native CLI (`garage`) for bucket + key bootstrap, drop `mc`

MinIO's `mc` client was used to create the bucket and validate access. Garage ships `garage bucket create`, `garage key create`, and `garage bucket allow` for the same operations. We drop `pkgs.minio-client`. For the S3 smoke-test validation step (upload/download/presign), we'll use `awscli2` since it's already a generic S3 client and useful elsewhere.

**Alternatives considered:**

- Keep `mc` pointing at Garage (mc is technically S3-generic) — works, but pulls in an unwanted MinIO-branded dep.
- Use `s5cmd` — fine but adds a dep we don't otherwise need.

### D3: Rename env vars `MINIO_TEST_S3_*` → `NCPS_TEST_S3_*`

Use backend-neutral names so the test contract doesn't carry the implementation's brand. Update `enable-s3-tests`, `nix/packages/ncps/default.nix` preCheck, and any Go test code that reads these vars.

**Alternatives considered:**

- Keep `MINIO_*` names — confusing once MinIO is gone; future swap requires another rename.
- Use `GARAGE_*` — same brand-coupling problem.

### D4: Garage listens only on the S3 API port (9000); drop console port 9001

Garage has no web console. We free up port 9001 and remove the `MINIO_CONSOLE*` env vars and the console URL section of the init script's banner.

### D5: K8s tests deploy Garage via its official Helm chart, or a minimal hand-written Deployment

Garage publishes an official container image (`dxflrs/garage`). For k8s-tests we keep the same shape as today's MinIO deployment: a single-replica Deployment + Service + initContainer that runs `garage layout assign` and bucket/key bootstrap. We do *not* introduce a Helm chart dependency — a small templated YAML in `nix/k8s-tests/` matches the existing pattern.

### D6: Keep test credentials stable (`GK1234567890abcdef12345678` / 64-char hex secret, `test-bucket`)

Garage requires key IDs that follow its `GK<hex>` format and secrets that are 64-character hex strings. The implementation pins `NCPS_TEST_S3_ACCESS_KEY_ID=GK1234567890abcdef12345678` and `NCPS_TEST_S3_SECRET_ACCESS_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`, with bucket name `test-bucket`. These stable values mean no Go test code needs updating beyond the env var rename from the MinIO names.

## Risks / Trade-offs

- **[Loss of web console]** → Mitigation: dev rarely needed the MinIO console; document `garage bucket list` / `garage stats` CLI usage in the init banner as the replacement.
- **[Garage layout step adds startup latency]** → Mitigation: layout commands are O(ms) on a single node; combine into one boot script and gate readiness on `garage status` rather than a fixed sleep.
- **[Presigned URL behavior diverges from MinIO/AWS edge cases]** → Mitigation: the existing init-script smoke test already covers presigned-URL upload/download; if it passes, our `pkg/storage/s3/` paths are covered. Run the full S3 integration test suite against Garage before merging.
- **[K8s image pull from Docker Hub rate limits]** → Mitigation: pre-load `dxflrs/garage` into the Kind cluster the same way the MinIO image is loaded today.
- **[Env var rename breaks anyone with `MINIO_*` exported in their shell]** → Mitigation: `enable-s3-tests` continues to do the right thing automatically; `disable-integration-tests` unsets both old and new names during a transition window; CLAUDE.md updated in the same change.
- **[Garage AGPL vs MinIO AGPL]** → No change in license posture for dev tooling.

## Migration Plan

This is dev/test infra — there is no production migration. Order of operations within the change:

1. Add `pkgs.garage` to dev shell; verify CLI works.
2. Write `start-garage.sh` and `init-garage.sh`; replace the two MinIO process-compose services. Verify `nix run .#deps` boots cleanly and `init-garage.sh` smoke test passes.
3. Rename env vars across `nix/process-compose/flake-module.nix`, `enable-s3-tests`, `disable-integration-tests`, `nix/packages/ncps/default.nix`, any Go test referencing `MINIO_*`. Drop `MINIO_CONSOLE*`.
4. Update `dev-scripts/run.sh s3` to use the new env vars.
5. Update `nix/k8s-tests/` config + manifest generation to deploy Garage; verify all 12 permutations pass.
6. Update `CLAUDE.md` and any other docs (README, dev-scripts comments) — replace MinIO references with Garage; update the "S3 Storage (MinIO)" section title and self-validation bullet points.
7. Rebuild `docker-dev` base image via `nix run .#update-cu-base`.
8. Run `nix flake check` and `k8s-tests all`; merge.

**Rollback:** revert the commit; no persistent state migration needed (all dev/test storage is ephemeral).

## Open Questions

- Do we want to keep the `nix/process-compose/init-minio.sh` smoke-test ergonomics 1:1 (banner with credentials, presigned-URL test, signed URL exhibition)? Default: yes, port to `init-garage.sh`.
- Should the env-var rename happen as a separate prep commit so the Garage swap diff is smaller? Default: single commit; the rename is part of the swap rationale and reviewers benefit from seeing them together.
- Garage version pin policy — track nixpkgs default, or pin in flake? Default: track nixpkgs default; Garage's S3 surface is stable.
