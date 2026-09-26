## MODIFIED Requirements

### Requirement: Failed job pods are retained for debugging

Every Job and CronJob rendered by the chart MUST default to `restartPolicy: Never` in its pod spec.
Under `restartPolicy: OnFailure` the Job controller deletes the pod as soon as the backoff limit is
reached, which destroys the logs of the run that failed; `Never` causes each attempt to land in its
own pod. For a plain Job, a failed pod then survives until the Job's TTL removes it.

Under a CronJob, retention is additionally bounded by the CronJob controller's history limits, which
the chart MUST set rather than leaving to the Kubernetes default of 1 failed Job. The rendered fsck
CronJob MUST retain more than one failed Job by default, so that a failed run's pod is not destroyed
by the next failure.

`ttlSecondsAfterFinished` and the history limits are independent cleanup mechanisms and whichever
fires first wins: a finished Job is removed when its TTL expires even if the history limit would
still have retained it, and is removed when it falls outside the history limit even if its TTL has
not expired.

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

#### Scenario: fsck cronjob retains more than one failed run by default

- **WHEN** the chart is rendered with `fsck.enabled=true` and no history-limit override
- **THEN** the rendered CronJob's `spec.failedJobsHistoryLimit` is greater than the Kubernetes
  default of 1

## ADDED Requirements

### Requirement: CronJob history limits are operator-configurable

The chart MUST expose the CronJob history limits for the fsck CronJob as
`fsck.job.failedJobsHistoryLimit` and `fsck.job.successfulJobsHistoryLimit`, rendering each into the
CronJob spec. When a value is null the chart MUST omit that field entirely so the Kubernetes default
applies. An explicit `0` MUST be rendered rather than treated as unset, since `0` is the meaningful
setting for "retain none".

These keys live on the per-job block rather than in `jobDefaults`, because history limits are
CronJob-only fields and would have no effect on the chart's three plain Jobs.

#### Scenario: Shipped defaults are rendered

- **WHEN** the chart is rendered with `fsck.enabled=true` and default values
- **THEN** the rendered CronJob's `spec.failedJobsHistoryLimit` is `3`
- **AND** its `spec.successfulJobsHistoryLimit` is `3`

#### Scenario: Operator overrides a history limit

- **WHEN** the chart is rendered with `fsck.enabled=true` and
  `fsck.job.failedJobsHistoryLimit=10`
- **THEN** the rendered CronJob's `spec.failedJobsHistoryLimit` is `10`

#### Scenario: Null omits the field

- **WHEN** the chart is rendered with `fsck.enabled=true`,
  `fsck.job.failedJobsHistoryLimit=null` and `fsck.job.successfulJobsHistoryLimit=null`
- **THEN** the rendered CronJob contains neither `failedJobsHistoryLimit` nor
  `successfulJobsHistoryLimit`

#### Scenario: Explicit zero is honored

- **WHEN** the chart is rendered with `fsck.enabled=true` and
  `fsck.job.successfulJobsHistoryLimit=0`
- **THEN** the rendered CronJob's `spec.successfulJobsHistoryLimit` is `0`
