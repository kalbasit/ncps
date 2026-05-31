## ADDED Requirements

### Requirement: The migrate-chunks-to-nar Helm Job MUST be a regular release resource, not a Helm hook

The Job rendered by `migrateChunksToNar.enabled: true` SHALL carry no `helm.sh/hook*` or `argocd.argoproj.io/hook*` annotations. It is a regular Kubernetes Job included in the Helm release manifest. ArgoCD syncs it alongside other resources and does not treat its outcome as a gate on sync success.

#### Scenario: Job is rendered without hook annotations

- **WHEN** the Helm chart is rendered with `migrateChunksToNar.enabled: true`
- **THEN** the resulting Job manifest SHALL NOT contain `helm.sh/hook`, `helm.sh/hook-weight`, or `helm.sh/hook-delete-policy` annotations

#### Scenario: Job is auto-deleted after completion

- **WHEN** the Job finishes (success or failure)
- **THEN** Kubernetes SHALL garbage-collect the Job after `migrateChunksToNar.job.ttlSecondsAfterFinished` seconds (default 3600)
