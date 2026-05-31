## 1. Fix migrate-chunks-to-nar Job

- [x] 1.1 Edit `charts/ncps/templates/migrate-chunks-to-nar-job.yaml` — remove the three `helm.sh/hook*` annotations and their comment block
- [x] 1.2 Edit `charts/ncps/values.yaml` — update the `migrateChunksToNar.enabled` comment to remove "Runs on each install/upgrade while enabled"
- [x] 1.3 Edit `charts/ncps/tests/migrate_chunks_to_nar_job_test.yaml` — rename "should create a hook Job when enabled" to "should create a Job when enabled" and remove the `metadata.annotations["helm.sh/hook"]` assertion

## 2. Add migrate-nar-to-chunks Job

- [x] 2.1 Create `charts/ncps/templates/migrate-nar-to-chunks-job.yaml` — mirror of the chunks-to-nar template: replace `migrateChunksToNar` → `migrateNarToChunks`, command arg `migrate-chunks-to-nar` → `migrate-nar-to-chunks`, remove `--force-reclaim` arg block, no `helm.sh/hook*` annotations
- [x] 2.2 Edit `charts/ncps/values.yaml` — add `migrateNarToChunks` section mirroring `migrateChunksToNar` (with `enabled: false`, `dryRun: false`, `concurrency: 10`, `resources: {}`, `securityContext`, `job.backoffLimit: 1`, `job.ttlSecondsAfterFinished: 3600`, `job.annotations/nodeSelector/tolerations/affinity`)
- [x] 2.3 Create `charts/ncps/tests/migrate_nar_to_chunks_job_test.yaml` — Helm unit test covering: not rendered by default, rendered when enabled (correct name + args), `--dry-run` passed through, no hook annotations present

## 3. Verify

- [x] 3.1 Run `helm unittest charts/ncps` — all tests pass
- [x] 3.2 Run `task fmt` and `task lint` — clean exit
