## ADDED Requirements

### Requirement: The Helm chart MUST provide an opt-in Job for migrate-nar-to-chunks

The chart SHALL render a Kubernetes Job that runs `ncps migrate-nar-to-chunks` when `migrateNarToChunks.enabled: true`. The Job SHALL be disabled by default (`enabled: false`). It SHALL carry no `helm.sh/hook*` or `argocd.argoproj.io/hook*` annotations — it is a regular release resource.

#### Scenario: Job is not rendered when disabled

- **WHEN** the Helm chart is rendered with `migrateNarToChunks.enabled: false` (the default)
- **THEN** no `migrate-nar-to-chunks` Job resource SHALL appear in the rendered manifests

#### Scenario: Job is rendered when enabled

- **WHEN** the Helm chart is rendered with `migrateNarToChunks.enabled: true`
- **THEN** a Job named `<release>-migrate-nar-to-chunks` SHALL be rendered
- **AND** its container SHALL run `/bin/ncps migrate-nar-to-chunks` with the configured storage, database, and Redis credentials
- **AND** the Job SHALL carry no `helm.sh/hook*` annotations

#### Scenario: Dry-run mode is passed through

- **WHEN** the Helm chart is rendered with `migrateNarToChunks.enabled: true` and `migrateNarToChunks.dryRun: true`
- **THEN** the Job container args SHALL include `--dry-run`

### Requirement: The migrate-nar-to-chunks Job MUST be auto-deleted after completion

The Job SHALL set `ttlSecondsAfterFinished` so Kubernetes garbage-collects it automatically. The default SHALL be 3600 seconds. Users MAY override via `migrateNarToChunks.job.ttlSecondsAfterFinished`.

#### Scenario: Job is deleted after TTL expires

- **WHEN** the Job finishes (success or failure)
- **THEN** Kubernetes SHALL delete the Job after `migrateNarToChunks.job.ttlSecondsAfterFinished` seconds

#### Scenario: TTL can be disabled

- **WHEN** `migrateNarToChunks.job.ttlSecondsAfterFinished` is unset (null/invalid)
- **THEN** the Job manifest SHALL omit `ttlSecondsAfterFinished`, leaving cleanup to the operator
