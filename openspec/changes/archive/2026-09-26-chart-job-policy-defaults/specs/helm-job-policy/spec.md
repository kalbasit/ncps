## Purpose

Defines how the Helm chart resolves the Kubernetes Job execution policy — `restartPolicy`,
`backoffLimit` and `ttlSecondsAfterFinished` — for every Job and CronJob it renders, so that
operators can tune retry and cleanup behavior entirely from values, and so that the pod of a failed
run is retained for inspection rather than deleted.

## ADDED Requirements

### Requirement: Failed job pods are retained for debugging

Every Job and CronJob rendered by the chart MUST default to `restartPolicy: Never` in its pod spec.
Under `restartPolicy: OnFailure` the Job controller deletes the pod as soon as the backoff limit is
reached, which destroys the logs of the run that failed; `Never` causes each attempt to land in its
own pod, and failed pods survive until the job's TTL removes them.

#### Scenario: Migration job renders restartPolicy Never

- **WHEN** the chart is rendered with `migration.enabled=true` and `migration.mode=job`
- **THEN** the rendered Job's `spec.template.spec.restartPolicy` is `Never`

#### Scenario: Chunk-migration jobs render restartPolicy Never

- **WHEN** the chart is rendered with `migrateChunksToNar.enabled=true`, and separately with
  `migrateNarToChunks.enabled=true`
- **THEN** each rendered Job's `spec.template.spec.restartPolicy` is `Never`

#### Scenario: fsck cronjob renders restartPolicy Never

- **WHEN** the chart is rendered with `fsck.enabled=true`
- **THEN** the rendered CronJob's
  `spec.jobTemplate.spec.template.spec.restartPolicy` is `Never`

### Requirement: Global job defaults with independent per-job override

The chart MUST expose a top-level `jobDefaults` block supplying `restartPolicy`, `backoffLimit` and
`ttlSecondsAfterFinished` to every Job and CronJob it renders, and each per-job block
(`migration.job`, `fsck.job`, `migrateChunksToNar.job`, `migrateNarToChunks.job`) MUST be able to
override any of those three keys independently of the others and of the other jobs. Resolution
order for each key is: the per-job value when it is non-null, otherwise the `jobDefaults` value.
Changing a `jobDefaults` key MUST affect only those jobs that have not overridden that key.

#### Scenario: Operator changes a global default

- **WHEN** the chart is rendered with `jobDefaults.backoffLimit=5` and no per-job `backoffLimit`
  override on `fsck.job`
- **THEN** the rendered fsck CronJob's `spec.jobTemplate.spec.backoffLimit` is `5`

#### Scenario: Per-job override wins over the global default

- **WHEN** the chart is rendered with `jobDefaults.backoffLimit=5` and
  `fsck.job.backoffLimit=2`
- **THEN** the rendered fsck CronJob's `spec.jobTemplate.spec.backoffLimit` is `2`

#### Scenario: One job's override does not leak to another

- **WHEN** the chart is rendered with `jobDefaults.ttlSecondsAfterFinished=1800` and
  `migration.job.ttlSecondsAfterFinished=60`, with both the migration job and the fsck cronjob
  enabled
- **THEN** the migration Job's `spec.ttlSecondsAfterFinished` is `60`
- **AND** the fsck CronJob's `spec.jobTemplate.spec.ttlSecondsAfterFinished` is `1800`

#### Scenario: Overriding one key leaves the others on their defaults

- **WHEN** the chart is rendered with `fsck.job.backoffLimit=4` and no other `fsck.job` policy
  override
- **THEN** the rendered fsck CronJob's `spec.jobTemplate.spec.backoffLimit` is `4`
- **AND** its `spec.jobTemplate.spec.ttlSecondsAfterFinished` still resolves from `jobDefaults`

### Requirement: A null at both levels omits the field

The chart MUST omit `backoffLimit` and `ttlSecondsAfterFinished` from the rendered manifest entirely
when both the per-job value and the `jobDefaults` value are null, so that the Kubernetes built-in
default applies. The chart MUST NOT render a placeholder such as `<no value>` for any unset policy
key, and `values.schema.json` MUST accept null for every one of these keys so that clearing them
passes schema validation.

#### Scenario: Both levels null omits backoffLimit

- **WHEN** the chart is rendered with `jobDefaults.backoffLimit=null` and
  `fsck.job.backoffLimit=null`
- **THEN** the rendered fsck CronJob contains no `backoffLimit` key
- **AND** the rendered output contains no `<no value>` text

#### Scenario: Both levels null omits ttlSecondsAfterFinished

- **WHEN** the chart is rendered with `jobDefaults.ttlSecondsAfterFinished=null` and
  `migration.job.ttlSecondsAfterFinished=null`
- **THEN** the rendered migration Job contains no `ttlSecondsAfterFinished` key, so the job is
  retained indefinitely

#### Scenario: Clearing a policy key passes schema validation

- **WHEN** the chart is linted with `jobDefaults.backoffLimit=null` and
  `jobDefaults.ttlSecondsAfterFinished=null`
- **THEN** `values.schema.json` validation succeeds

### Requirement: An explicit zero is honored, not dropped

The chart MUST render an explicitly configured `0` for `backoffLimit` and
`ttlSecondsAfterFinished` rather than treating it as unset. A `ttlSecondsAfterFinished` of `0` means
the job becomes eligible for deletion immediately after it finishes, and a `backoffLimit` of `0`
means no retry is attempted; both are meaningful settings distinct from omitting the key.

#### Scenario: Zero TTL is rendered on the migration job

- **WHEN** the chart is rendered with `migration.job.ttlSecondsAfterFinished=0`
- **THEN** the rendered Job's `spec.ttlSecondsAfterFinished` is `0`

#### Scenario: Zero backoff limit is rendered

- **WHEN** the chart is rendered with `jobDefaults.backoffLimit=0` and no per-job override
- **THEN** each rendered Job's `backoffLimit` is `0`

### Requirement: Shipped defaults preserve the current rendered policy values

The values shipped with the chart MUST leave the rendered `backoffLimit` and
`ttlSecondsAfterFinished` of every job unchanged from before this capability existed, so that
adopting the new `jobDefaults` indirection is not itself a behavioral change for operators who set
nothing. Only `restartPolicy` changes, and only from `OnFailure` to `Never`.

#### Scenario: Default render is unchanged for retry and cleanup keys

- **WHEN** the chart is rendered with default values and each job enabled in turn
- **THEN** the migration Job renders `backoffLimit: 3` and `ttlSecondsAfterFinished: 300`
- **AND** the fsck CronJob, the migrate-chunks-to-nar Job and the migrate-nar-to-chunks Job each
  render `backoffLimit: 1` and `ttlSecondsAfterFinished: 3600`
