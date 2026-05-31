## Context

Two CLI commands exist for CDC migration (`pkg/ncps/`):

| Command | Direction | Helm Job |
|---------|-----------|----------|
| `migrate-chunks-to-nar` | chunks → whole NAR (exit CDC) | `migrate-chunks-to-nar-job.yaml` ✓ (but wrongly a hook) |
| `migrate-nar-to-chunks` | whole NAR → chunks (enter CDC) | **missing** |

The `migrate-chunks-to-nar` Job carries three Helm hook annotations that make ArgoCD treat it as a sync hook:

```yaml
"helm.sh/hook": post-install,post-upgrade
"helm.sh/hook-weight": "5"
"helm.sh/hook-delete-policy": before-hook-creation
```

This caused two OOMKill incidents that left the hook Job in a failed state, blocking all subsequent ArgoCD syncs until the Job was manually deleted. The Job already has `ttlSecondsAfterFinished: 3600` in `values.yaml` — Kubernetes cleanup is already wired up.

Note: `migration-job.yaml` (DB schema migration, `ncps migrate up`) uses `helm.sh/hook` correctly — it is short-lived, must gate the sync, and is out of scope.

## Goals / Non-Goals

**Goals:**
- Remove the three `helm.sh/hook*` annotations from `migrate-chunks-to-nar-job.yaml`
- Add a new `migrate-nar-to-chunks-job.yaml` Helm Job template (mirrors the existing one)
- Add a `migrateNarToChunks` values section (mirrors `migrateChunksToNar`, `enabled: false` by default)
- Update `migrateChunksToNar.enabled` comment in `values.yaml`
- Update existing Helm unit test; add new test for the nar-to-chunks Job

**Non-Goals:**
- Replacing hook annotations with ArgoCD-native equivalents (same failure mode)
- Modifying `migration-job.yaml` (DB schema migration — correct as-is)
- Changing `ttlSecondsAfterFinished` defaults (3600 s is already correct)
- Any Go code changes

## Decisions

### Remove hook annotations entirely — no replacement

| Option | Rejected because |
|--------|-----------------|
| Replace with `argocd.argoproj.io/hook: PostSync` | Same failure mode: gates syncs, re-runs on every deploy |
| Keep as hook but raise ArgoCD sync timeout | Doesn't prevent OOMKills blocking syncs |
| Add `argocd.argoproj.io/ignore-differences` | Hides the symptom; hook semantics still wrong |

As a regular Job the resource appears in the ArgoCD sync graph when `enabled: true` and disappears when disabled. Failure is visible in the UI but does not gate the sync.

### Mirror the existing template for `migrate-nar-to-chunks`

The two commands have nearly identical flag surfaces (storage, DB, Redis/lock, `--dry-run`; `migrate-chunks-to-nar` additionally has `--force-reclaim`). The new template is a direct copy of the existing one with:
- `migrateChunksToNar` → `migrateNarToChunks` throughout
- `migrate-chunks-to-nar` → `migrate-nar-to-chunks` in the Job name and container args
- No `--force-reclaim` arg block (the forward migration has no such flag)

No `helm.sh/hook*` annotations on the new Job — it is a regular resource from the start.

### `ttlSecondsAfterFinished: 3600` as default for both Jobs

Both Jobs are long-running and one-time. Auto-cleanup after 1 hour prevents stale Jobs from accumulating. Users can override via `migrateNarToChunks.job.ttlSecondsAfterFinished`.

## Risks / Trade-offs

- **[ArgoCD OutOfSync on existing clusters with hook Job still present]** → On next sync, ArgoCD sees the Job as a new regular managed resource if `migrateChunksToNar.enabled: true`. No data loss; ArgoCD reconciles normally. Mitigation: note in release.

- **[Helm unit tests break]** → `charts/ncps/tests/migrate_chunks_to_nar_job_test.yaml` likely asserts hook annotations. Must be updated to assert absence. Mitigation: handled in this PR.

## Migration Plan

1. Edit `charts/ncps/templates/migrate-chunks-to-nar-job.yaml` — remove the three `helm.sh/hook*` lines and their comment
2. Add `charts/ncps/templates/migrate-nar-to-chunks-job.yaml` — new Job template (no hook annotations)
3. Edit `charts/ncps/values.yaml` — add `migrateNarToChunks` section; update `migrateChunksToNar.enabled` comment
4. Edit `charts/ncps/tests/migrate_chunks_to_nar_job_test.yaml` — remove hook annotation assertions
5. Add `charts/ncps/tests/migrate_nar_to_chunks_job_test.yaml` — new Helm unit test
6. Run `helm unittest charts/ncps` to verify

Rollback: revert the commit. No data migration involved.
