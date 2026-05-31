## Context

The ncps Helm chart has two problems with security contexts:

1. **`create-db-dir` init container** (deployment, statefulset, migration-job, fsck-cronjob): hardcodes `runAsUser: 1000 / runAsGroup: 1000 / runAsNonRoot: true` directly in the template, overriding pod-level `podSecurityContext`. Operators who change `podSecurityContext.runAsUser` still get UID 1000 for this container.

2. **Per-container securityContext defaults**: `migration.securityContext`, `fsck.securityContext`, `migrateChunksToNar.securityContext`, and `migrateNarToChunks.securityContext` all default `runAsUser: 1000 / runAsGroup: 1000`, duplicating `podSecurityContext` and preventing a single-knob override. The main `securityContext` and `podSecurityContext` also carry defaults operators may not want.

The desired state: no opinionated identity defaults anywhere; operators set identity once and it propagates everywhere.

## Goals / Non-Goals

**Goals:**
- Add `containerDefaults.securityContext` — a global fallback applied to every container, deep-merged under per-container values
- Add `initImage.securityContext` — configurable security context for the busybox init container, also falling back to `containerDefaults.securityContext`
- Remove all UID/GID/fsGroup defaults from `podSecurityContext`, `securityContext`, and all per-container securityContext blocks
- Wire all seven containers to use deep merge: per-container → `containerDefaults.securityContext`

**Non-Goals:**
- Blind full-container merge for `containerDefaults` (would conflict with chart-controlled fields like `image`, `command`, `args`)
- `containerDefaults.resources` or `containerDefaults.env` (future extension, not this change)
- Changing any Go code or database migrations

## Decisions

### `containerDefaults` namespace, not a flat key

`containerDefaults.securityContext` groups under a namespace so future per-container defaults (`resources`, `env`) have a natural home without a breaking rename. Industry precedent: Bitnami uses `commonLabels`/`commonAnnotations`; cert-manager uses `global`.

### Deep merge, not simple `default` fallback

`merge $perContainer $global` (sprig): per-container keys win, global fills missing keys. This lets an operator set `allowPrivilegeEscalation: false` globally and override only `readOnlyRootFilesystem: false` on one specific container — without repeating all other global fields.

Simple `default` would require the per-container block to repeat every global field to add one override, which defeats the purpose.

### `initImage.securityContext` also falls back to `containerDefaults.securityContext`

The busybox init container has a different image but the same security requirements. Falling back to `containerDefaults.securityContext` means operators set once and all containers (including init) follow. Operators can still override just the init container via `initImage.securityContext`.

### No defaults anywhere

`containerDefaults.securityContext: {}`, `initImage.securityContext: {}`, `podSecurityContext: {}`, and all per-container securityContext blocks default to empty. Operators explicitly opt into any security posture. Documented in release notes as a breaking change.

## Risks / Trade-offs

- **[Breaking change for bare installations]** → Containers will run as root and without hardening if operators don't set `containerDefaults.securityContext` or `podSecurityContext`. Mitigation: release notes with migration guide; provide a recommended values snippet.

- **[`merge` on nil panics in some Helm versions]** → Guard both sides with `default dict`: `merge (default dict .Values.x.securityContext) (default dict .Values.containerDefaults.securityContext)`. Always produces a valid (possibly empty) map.

- **[Empty map renders ugly `securityContext: {}`]** → Wrap with `{{- with $ctx }}` so empty map produces no key at all — clean YAML.

## Migration Plan

For each operator upgrading, the recommended `values.yaml` override to restore previous behaviour:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 1000
  runAsGroup: 1000
  fsGroup: 1000
  fsGroupChangePolicy: OnRootMismatch
  seccompProfile:
    type: RuntimeDefault

containerDefaults:
  securityContext:
    allowPrivilegeEscalation: false
    capabilities:
      drop: [ALL]
    readOnlyRootFilesystem: true
    runAsNonRoot: true
    runAsUser: 1000
    runAsGroup: 1000
```
