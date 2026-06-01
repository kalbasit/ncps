## Why

The `create-db-dir` busybox init container (SQLite database directory creation) has its `runAsUser`, `runAsGroup`, and `runAsNonRoot` hardcoded in four chart templates. These container-level values override the pod-level `podSecurityContext`, so an operator who changes `podSecurityContext.runAsUser` to match their storage volume ownership will still have this init container running as UID 1000 — causing permission errors when creating the SQLite directory on a volume not owned by 1000.

All main/job containers (`securityContext`, `migration.securityContext`, `fsck.securityContext`, `migrateChunksToNar.securityContext`, `migrateNarToChunks.securityContext`) are already values-driven. The `create-db-dir` init container is the only exception, and it appears in four templates.

## What Changes

- Remove all default values from `podSecurityContext`, `securityContext`, and all per-container securityContext blocks (`migration`, `fsck`, `migrateChunksToNar`, `migrateNarToChunks`) — operators explicitly opt in to any security posture
- Add `containerDefaults.securityContext: {}` as a global fallback applied to every container via deep-merge; per-container values win on conflicts
- Add `initImage.securityContext: {}` to `values.yaml` (no defaults); deep-merged over `containerDefaults.securityContext` for the `create-db-dir` init container
- Replace the hardcoded securityContext block in `create-db-dir` in all four templates with the deep-merge pattern: `mergeOverwrite (deepCopy containerDefaults.securityContext) initImage.securityContext`

## Capabilities

### Modified Capabilities

- **helm-migration-job-volumes**: the `create-db-dir` init container security context is now operator-configurable via `initImage.securityContext`; UID/GID inherit from `podSecurityContext` by default rather than being hardcoded to 1000

## Impact

- `charts/ncps/templates/deployment.yaml` — replace hardcoded `create-db-dir` securityContext
- `charts/ncps/templates/statefulset.yaml` — same
- `charts/ncps/templates/migration-job.yaml` — same
- `charts/ncps/templates/fsck-cronjob.yaml` — same (nindent 16 due to CronJob nesting)
- `charts/ncps/values.yaml` — add `initImage.securityContext` defaults
- `charts/ncps/tests/` — update tests asserting the init container securityContext
- No Go code changes; no database migrations
