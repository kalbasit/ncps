## Why

The `migrate-chunks-to-nar` Kubernetes Job is currently annotated as an ArgoCD sync hook, which caused two OOMKill failures today that blocked all subsequent ArgoCD syncs on ncps until the hook was manually deleted. Hook semantics are wrong for a long-running, one-time data migration that is deliberately opt-in — it belongs in the regular sync manifests, not the sync lifecycle.

Additionally, the reverse migration (`migrate-nar-to-chunks`, which chunks whole NARs into CDC pieces to enable CDC) has no Helm Job at all — operators have no supported way to run it via the chart.

## What Changes

- Remove Helm hook annotations (`helm.sh/hook`, `helm.sh/hook-weight`, `helm.sh/hook-delete-policy`) from the `migrate-chunks-to-nar` Job in the Helm chart; the Job becomes a regular release resource
- Add a new `migrate-nar-to-chunks` Helm Job (mirrors the existing one) for the forward CDC migration direction, with the same opt-in pattern (`migrateNarToChunks.enabled: false` by default)
- `ttlSecondsAfterFinished` is already defaulted to 3600 s in `values.yaml` for `migrate-chunks-to-nar`; apply the same default for the new `migrate-nar-to-chunks` Job

## Capabilities

### New Capabilities

- **migrate-nar-to-chunks Helm Job**: operators can enable `migrateNarToChunks.enabled: true` to run the forward CDC migration (whole NARs → chunks) as a standalone Kubernetes Job via the Helm chart.

### Modified Capabilities

- **chunks-to-nar-migration**: the migration job is no longer a Helm sync hook; it is a regular Job included in the Helm release when `migrateChunksToNar.enabled: true`. ArgoCD will create it on sync (if enabled) but will not treat its outcome as a gate on sync success.

## Impact

- `charts/ncps/templates/migrate-chunks-to-nar-job.yaml` — remove three `helm.sh/hook*` annotations
- `charts/ncps/templates/migrate-nar-to-chunks-job.yaml` — new file (mirrors chunks-to-nar template)
- `charts/ncps/values.yaml` — new `migrateNarToChunks` section; update `migrateChunksToNar.enabled` comment
- `charts/ncps/tests/` — update existing test; add new test for `migrate-nar-to-chunks` Job
- `openspec/specs/chunks-to-nar-migration/` — update deployment-mechanism spec to reflect hook removal
- No Go code changes; no database migrations; no I/O or network impact
