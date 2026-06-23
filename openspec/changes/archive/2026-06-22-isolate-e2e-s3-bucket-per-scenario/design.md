## Context

The kubernetes e2e harness (`nix/e2e-tests/src/k8s_tests.py`) provisions one Kind cluster and then runs every catalog scenario against it sequentially. It already isolates the relational database per scenario (`ncps_<name>`, created/dropped per scenario), but all scenarios share a single Garage S3 bucket `ncps-bucket`, created once during garage setup (`k8s_tests.py:360-372`) and never cleaned. `get_cluster_creds()` (`k8s_tests.py:838-845`) returns this fixed bucket; the per-scenario Helm values are generated from those creds (`config.nix` `inherit (s3) bucket`), and the S3 validation check (`k8s_tests_tester.py:_test_s3_storage`) lists `store/chunk/` in that same bucket.

Because the six test narinfo hashes are shared across scenarios, a non-CDC scenario (e.g. `single-s3-postgres`) downloads and stores those NARs as whole-file `.nar.xz` in the shared bucket. A later CDC scenario runs with a fresh database but the dirty bucket; since PR #1393, ncps treats a stored `.nar.xz` as a present `Compression:none` NAR, so it skips the upstream download and eager chunking and `BackgroundMigrateNarToChunks` fails with "error fetching nar from store: not found" — zero chunk rows are created and the validation (`SELECT COUNT(*) FROM chunks == 0`) fails. This was confirmed locally: emptying the bucket makes `single-s3-postgres-cdc` pass with 4 chunks; the dirty bucket fails with 0.

## Goals / Non-Goals

**Goals:**
- Each kubernetes scenario deploys against clean, isolated S3 storage, mirroring the existing per-scenario database isolation.
- The S3 validation check reflects only the running scenario's writes (no false positives from residue).
- Restore the `e2e-nightly` kubernetes leg to green.

**Non-Goals:**
- No ncps production code, Helm chart, or CDC behavior changes. The residual-whole-file handling in ncps is out of scope (it is an artificial fresh-DB-over-dirty-storage state, not a realistic production path; the realistic enable-CDC-on-existing-cache path is `migrate-nar-to-chunks`, covered by `cdc-lifecycle`).
- No change to `local` mode (its scenarios use local per-pod storage and do not share an S3 bucket).

## Decisions

### Decision: Per-scenario bucket, not clean-the-shared-bucket

Give each S3-using scenario its own bucket `ncps-<scenario-name>` (kebab-case scenario names are valid S3 bucket names — lowercase + hyphens, well under 63 chars), mirroring the per-scenario `ncps_<name>` databases.

**Alternative considered — empty the shared bucket before each scenario:** rejected. The Garage image is distroless (no shell, no S3 CLI), so emptying objects would require standing up an S3 client (boto3) with a port-forward before every scenario — fragile, and not parallel-safe. Per-scenario buckets need no teardown, are race-free, and are symmetric with the database isolation already in place.

**Alternative considered — per-scenario S3 *prefix* instead of per-scenario bucket:** rejected as incomplete. `config.nix` already sets `config.storage.s3.prefix = perm.name` per scenario, but (a) the Helm chart configmap (`charts/ncps/templates/configmap.yaml`) does not render `storage.s3.prefix` at all, so it is silently dropped, and (b) even if rendered, the **chunk store ignores the configured prefix** — `pkg/storage/chunk/s3.go` builds keys as `store/chunk/<hash>` unconditionally (`chunkPath`, ignoring `cfg.Prefix`), while only the NAR/narinfo store honors the prefix. So a prefix fix would isolate NAR/narinfo but leave chunks colliding across scenarios, and would also require touching production code (chart + ncps chunk store). A per-scenario **bucket** isolates every store (NAR, narinfo, chunk) at once because the bucket name is the one input all three stores honor, with no production-code change. (The now-dead `prefix = perm.name` in `config.nix` is harmless and left as-is; removing it is out of scope.)

### Decision: Create + grant buckets idempotently during garage setup

Where the harness creates the single `ncps-bucket` today, iterate the catalog permutations (the same source already used to create per-test databases) and, for each scenario whose storage type is `s3`, create `ncps-<name>` and grant the existing shared access key (`GK1234...`) read+write+owner — all idempotent (`bucket info` guard, `bucket allow` is idempotent). Keep using the single shared access key; only the bucket is per-scenario.

### Decision: Inject the per-scenario bucket into value generation and validation

The per-scenario bucket name flows to the deployment exactly where the shared bucket does today: the value-generation path that reads `creds["s3"]["bucket"]`. Set the bucket per scenario (e.g. derive `ncps-<name>` at value-generation time, or thread it through `get_cluster_creds`). Because `_test_s3_storage` reads the bucket from the same per-deployment S3 config, the validation check automatically targets the correct per-scenario bucket with no separate change.

### Decision: Bucket name derivation

`ncps-` + the scenario's catalog name verbatim (already kebab-case), e.g. `ncps-single-s3-postgres-cdc`. This is unique per scenario and trivially mappable back to the scenario for debugging.

## Risks / Trade-offs

- **Many buckets on single-node Garage** → negligible: Garage handles many buckets cheaply at test scale; buckets are created once per cluster and reused across runs.
- **A scenario still pointing at the old shared bucket** → mitigation: remove/replace the shared `ncps-bucket` usage so there is a single source for the bucket name (the per-scenario derivation), and assert in the harness unit tests that generated values carry the per-scenario bucket.
- **Stale residue in a long-lived dev Kind cluster** → per-scenario buckets are still cleaner than the shared one, but a re-run of the *same* scenario reuses its bucket; this matches the database behavior and is acceptable for nightly (fresh-ish cluster). Not a regression vs. today.

## Migration Plan

Test-harness-only change; no production deploy. Validation:
1. Run the affected scenarios locally on Kind (`nix run .#e2e -- --mode kubernetes --scenario single-s3-postgres-cdc`, then `ha-s3-postgres-cdc`, `ha-s3-postgres-cdc-lifecycle`) and confirm non-zero chunk counts.
2. Run the harness pytest unit net (`flake check` e2e-harness-unit) for the value-generation/bucket logic.
3. Trigger `e2e-nightly` via `workflow_dispatch` on the branch and confirm the kubernetes leg goes green.

Rollback: revert the harness change.

## Open Questions

- None. The fix location and mechanism are confirmed by the local reproduction.
