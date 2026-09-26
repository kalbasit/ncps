## Why

The fsck CronJob sets neither `failedJobsHistoryLimit` nor `successfulJobsHistoryLimit`, so
Kubernetes applies its own defaults — 1 and 3 respectively
(`pkg/apis/batch/v1/defaults.go`). A `failedJobsHistoryLimit` of 1 means the CronJob controller
deletes an older failed Job, and its pods, as soon as a newer run fails. That directly undercuts the
goal of the `helm-job-policy` capability: `restartPolicy: Never` was adopted so a failed run's pod
survives for inspection, and an implicit history limit of 1 throws it away anyway. The preceding
change only qualified the spec wording to admit this; this change fixes the behavior and puts the
limit under operator control.

## What Changes

- Add `fsck.job.failedJobsHistoryLimit` and `fsck.job.successfulJobsHistoryLimit`, rendered into the
  fsck CronJob spec. A `null` at either omits the field so the Kubernetes default applies.
- Ship `failedJobsHistoryLimit: 3`. **This is a deliberate behavior change** from the Kubernetes
  default of 1: a failed fsck run now survives two further failures instead of being deleted by the
  next one.
- Ship `successfulJobsHistoryLimit: 3`, matching the Kubernetes default — no behavior change, but the
  value becomes explicit and tunable.
- Document that `ttlSecondsAfterFinished` and the history limits are independent cleanup mechanisms
  and whichever fires first wins. fsck inherits `jobDefaults.ttlSecondsAfterFinished: 3600`, so a
  failed fsck Job is still deleted an hour after it finishes regardless of the history limit; the
  limit becomes the binding control only once an operator clears the TTL at both levels.
- Update the CronJob retention wording in the `helm-job-policy` spec, `values.yaml` and the Chart
  Reference, now that the limit is chart-set rather than an implicit Kubernetes default.
- Add `CHANGELOG.md` entries under `[Unreleased]` covering the whole of PR #1514, of which this is
  the final piece.

## Capabilities

### New Capabilities

<!-- none: this extends an existing capability -->

### Modified Capabilities

- `helm-job-policy`: the retention requirement currently describes CronJob retention as bounded by
  an implicit Kubernetes history limit. It gains a chart-rendered, operator-configurable history
  limit, and the requirement is restated to cover both the new values and how they compose with
  `ttlSecondsAfterFinished`.

## Impact

- `charts/ncps/templates/fsck-cronjob.yaml` — renders the two new fields.
- `charts/ncps/values.yaml` — two new `fsck.job` keys plus the composition note.
- `charts/ncps/values.schema.json` — schema entries accepting integer or null.
- `charts/ncps/tests/job_policy_test.yaml` — scenarios for defaults, override and omit-on-null.
- `docs/docs/User Guide/Installation/Helm Chart/Chart Reference.md` — reference rows and the
  cleanup-interaction note.
- `openspec/specs/helm-job-policy/spec.md` — restated retention requirement.
- `CHANGELOG.md` — `[Unreleased]` entries for PR #1514.
- Affects only the fsck CronJob. The three plain Jobs, and all Go code, are untouched.
