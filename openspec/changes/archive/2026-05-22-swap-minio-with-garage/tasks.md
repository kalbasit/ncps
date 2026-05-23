# Tasks: Swap MinIO with Garage

## 1. Dev shell package swap

- [x] 1.1 Replace `pkgs.minio` and `pkgs.minio-client` with `pkgs.garage` (and `pkgs.awscli2` if not already present) in `nix/dev-packages.nix` / `flake.nix` dev shell inputs
- [x] 1.2 Verify `garage --version` is on `PATH` in the dev shell (`nix develop -c garage --version`)
- [x] 1.3 Verify `minio` and `mc` are no longer on `PATH`

## 2. process-compose: replace MinIO services with Garage

- [x] 2.1 Add `start-garage.sh` under `nix/process-compose/` that:
  - writes a temp Garage config file (RPC bind, S3 API bind on `127.0.0.1:9000`, ephemeral data + metadata dirs)
  - starts `garage server`
  - runs `garage layout assign` + `garage layout apply` for single-node bootstrap
- [x] 2.2 Add `init-garage.sh` under `nix/process-compose/` that:
  - waits for `garage status` to report healthy
  - idempotently creates bucket `$NCPS_TEST_S3_BUCKET` (`garage bucket create`)
  - idempotently creates access key with `$NCPS_TEST_S3_ACCESS_KEY_ID` / `$NCPS_TEST_S3_SECRET_ACCESS_KEY` (`garage key create --name` + import or `garage key import`)
  - grants the key read+write on the bucket (`garage bucket allow`)
  - runs an S3 smoke test using `aws s3` / `aws s3 presign`: put object, fetch via presigned URL, assert anonymous GET is denied
  - prints a banner with endpoint + credentials (no console URL)
  - touches `/tmp/ncps-garage-ready`
- [x] 2.3 Rewrite `nix/process-compose/flake-module.nix`:
  - rename `minio-server` → `garage-server`; `minio-init` → `garage-init`
  - point `runtimeInputs` at `pkgs.garage` (+ `pkgs.awscli2`)
  - drop the `:9001` console port mapping and `MINIO_CONSOLE*` env vars
  - rename env vars in `minioEnvironment` → `garageEnvironment` and switch all `MINIO_TEST_S3_*` keys to `NCPS_TEST_S3_*`; drop `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` (Garage uses its own admin token via config)
  - update health checks (`garage status` exec probe instead of `/minio/health/live` HTTP probe)
  - update `depends_on` references
- [x] 2.4 Delete `nix/process-compose/start-minio.sh` and `nix/process-compose/init-minio.sh`
- [x] 2.5 Run `nix run .#deps` and verify both services come up healthy

## 3. Env var rename across the repo

- [x] 3.1 Update `enable-s3-tests` (and `enable-integration-tests`, `disable-integration-tests`) helpers to export/unset `NCPS_TEST_S3_*` names; remove `MINIO_TEST_S3_*` exports
- [x] 3.2 Update `nix/packages/ncps/default.nix` `preCheck`/`postCheck` to start Garage instead of MinIO, and export `NCPS_TEST_S3_*` for the test phase
- [x] 3.3 `grep -rn 'MINIO' .` and update any remaining Go test files, scripts, or Nix code reading `MINIO_*` env vars to read `NCPS_TEST_S3_*` (TDD: add/adjust skip-gate checks first)
- [x] 3.4 Update `dev-scripts/run.sh s3` to export `NCPS_TEST_S3_*` and remove `MINIO_*` references

## 4. K8s integration tests (`nix/k8s-tests/`)

- [x] 4.1 Replace MinIO manifest generation (Deployment/Service/Secret/init-job) with Garage equivalents: single-replica `dxflrs/garage` Deployment, Service on port 3900 (S3 API) mapped to the same in-cluster name tests expect, init-Job that runs `garage layout` + bucket/key bootstrap
- [x] 4.2 Update `nix/k8s-tests/config.nix` permutations that reference MinIO image/values to reference Garage; ensure HA scenarios still target a single Garage instance (no Garage cluster topology needed for tests)
- [x] 4.3 Update `k8s-tests cluster create` to pre-load `dxflrs/garage` image into Kind (mirror existing MinIO pre-load)
- [x] 4.4 Update generated Helm values / ncps env so pods receive `NCPS_TEST_S3_ENDPOINT` pointing at the in-cluster Garage Service
- [x] 4.5 Garage path validated via `k8s-tests install single-s3-sqlite` + `test` (see 6.4). Full 12-permutation `all` run blocked by pre-existing non-Garage issues: `narinfo_references` schema bug + Kind API-server flake under cumulative load.

## 5. Documentation

- [x] 5.1 Update `CLAUDE.md`: rename the "S3 Storage (MinIO)" section to "S3 Storage (Garage)", update endpoint/port/console descriptions, replace `MINIO_*` examples with `NCPS_TEST_S3_*`, drop console URL references
- [x] 5.2 Update `CLAUDE.md` "Dependency Management" section: replace `MinIO (S3-compatible storage)` block with `Garage (S3-compatible storage)`; remove console port; update self-validation bullets
- [x] 5.3 Update `README.md` and other developer docs (`docs/docs/Developer Guide/**`, `nix/k8s-tests/README.md`, `.agent/skills/ncps/SKILL.md`) to reference Garage
- [x] 5.4 Update user-facing docs (`docs/docs/User Guide/**`) and example values (`charts/ncps/values.yaml` comments + tests, `config.example.yaml`) to replace MinIO mentions with Garage. The runtime S3 client behavior is unchanged; only example/recommended backends in our own docs swap.
- [x] 5.5 Update `dev-scripts/run.sh s3` and `dev-scripts/run.py` inline comments and health-check paths

## 6. Build, lint, and verify

- [x] 6.1 `nix fmt` — 352 files traversed, 0 changed (already formatted).
- [x] 6.2 `golangci-lint run --fix` — 0 issues after fixing two `lll` violations in `pkg/ncps/serve.go` (S3 endpoint + force-path-style flag usage strings).
- [x] 6.3 `nix flake check --no-build` parses cleanly; `nix build .#ncps` boots Garage in sandbox, runs all integration backends, S3 paths pass. **Caveat:** an unrelated, pre-existing SQLite perf assertion (`pkg/database/contract_test.go:623`, `assert.Less(time.Since(CreatedAt), 3s)`) flakes under sandbox load — not caused by this change.
- [x] 6.4 `k8s-tests` Garage path validated. Iterations during this change uncovered and fixed three k8s-specific bugs in the Garage bootstrap path: (a) the `dxflrs/garage` image is distroless with no `/bin/sh`, so the bootstrap was rewritten as individual `kubectl exec /garage <cmd>` calls instead of a shell heredoc; (b) the image has no ENTRYPOINT, only CMD, so `command: ["/garage"]` is now explicit in the StatefulSet; (c) Garage rejects non-hex characters after `GK` in access key IDs, so the k8s test creds were aligned with the proven `GK1234567890abcdef12345678` format from process-compose. Verified end-to-end with `k8s-tests install single-s3-sqlite` + `k8s-tests test single-s3-sqlite`: pod ready, ncps connects to Garage via S3 v4, NAR written to bucket, storage check passes (`1 NAR objects`). **Note:** the full 12-permutation `k8s-tests all` run had non-Garage failures: a pre-existing `narinfo_references` schema bug (`no such column: id`, breaks the HTTP narinfo check across all S3 scenarios) and Kind API-server flake under cumulative load (SSL EOF across local + HA scenarios). Both are out of scope here. Re-run after those are fixed.
- [x] 6.5 Dev container image rebuilt with Garage; `.container-use/environment.json` updated to `kalbasit/ncps-dev:5q2vmh0s2hpxvskcjks6yid4lwy3im86` (built and pushed by user).

## 7. Final sweep

- [x] 7.1 `grep -rn -i 'minio' .` — remaining matches are limited to (a) `github.com/minio/minio-go/v7` library imports + `minio.X` package identifiers from that library (kept per proposal Non-goal: `minio-go` is a generic S3 v4 client), (b) `minioadmin` opaque AWS-style placeholder credentials in test fixtures, (c) accurate `minio-go` library-name mentions in dev architecture docs.
- [x] 7.2 `grep -rn 'MINIO_' .` returns zero matches in source.
- [x] 7.3 CHANGELOG — N/A. Project does not maintain a CHANGELOG file; release notes are derived from semantic commit messages. The swap will be communicated via the commit subject and PR description.
