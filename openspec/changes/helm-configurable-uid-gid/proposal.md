## Why

The `create-db-dir` busybox init container (SQLite database directory creation) has its `runAsUser`, `runAsGroup`, and `runAsNonRoot` hardcoded in four chart templates. These container-level values override the pod-level `podSecurityContext`, so an operator who changes `podSecurityContext.runAsUser` to match their storage volume ownership will still have this init container running as UID 1000 — causing permission errors when creating the SQLite directory on a volume not owned by 1000.

All main/job containers (`securityContext`, `migration.securityContext`, `fsck.securityContext`, `migrateChunksToNar.securityContext`, `migrateNarToChunks.securityContext`) are already values-driven. The `create-db-dir` init container is the only exception, and it appears in four templates.

## What Changes

- Add `initImage.securityContext` to `values.yaml` with defaults that preserve the current hardened posture (`allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`, `readOnlyRootFilesystem: true`) — intentionally omitting `runAsUser`/`runAsGroup`/`runAsNonRoot` so they inherit from `podSecurityContext`
- Replace the hardcoded securityContext block in the `create-db-dir` init container in all four affected templates with `{{- toYaml .Values.initImage.securityContext | nindent <N> }}`

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
