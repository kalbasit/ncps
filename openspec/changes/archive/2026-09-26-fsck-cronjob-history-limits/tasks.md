## 1. Red — tests for the history limits

- [x] 1.1 Add default-render scenarios to `charts/ncps/tests/job_policy_test.yaml` asserting the
  fsck CronJob renders `spec.failedJobsHistoryLimit: 3` and `spec.successfulJobsHistoryLimit: 3`,
  and verify they fail because neither field is rendered today
- [x] 1.2 Add an override scenario (`fsck.job.failedJobsHistoryLimit=10`) and verify it fails
- [x] 1.3 Add an omit-on-null scenario (both keys null → neither field present) and verify it
  currently passes for the wrong reason (nothing is rendered at all), so it will still guard after
  the change
- [x] 1.4 Add an explicit-zero scenario (`fsck.job.successfulJobsHistoryLimit=0` renders `0`) and
  verify it fails

## 2. Green — render the limits

- [x] 2.1 Add `failedJobsHistoryLimit: 3` and `successfulJobsHistoryLimit: 3` to the `fsck.job`
  block in `values.yaml`, next to `concurrencyPolicy`, documenting that null omits the field and
  that the TTL and history limits are independent with whichever fires first winning; verify
  `helm template` still renders
- [x] 2.2 Render both fields in `charts/ncps/templates/fsck-cronjob.yaml` guarded by
  `kindIs "invalid"` (not truthiness, so an explicit `0` survives), and verify all group 1
  scenarios now pass
- [x] 2.3 Add both keys to `charts/ncps/values.schema.json` as `["integer", "null"]` with
  `minimum: 0`, and verify `helm lint` passes with the keys set to null and to 0

## 3. Documentation

- [x] 3.1 Update the retention wording in `openspec/specs/helm-job-policy/spec.md` via the change's
  delta so CronJob retention refers to the chart-set limit, and verify
  `openspec validate --specs` passes
- [x] 3.2 Add the two reference rows and a cleanup-interaction note to
  `docs/docs/User Guide/Installation/Helm Chart/Chart Reference.md`, and verify the fsck table lists
  both new keys
- [x] 3.3 Update the CronJob retention note in `values.yaml`'s `jobDefaults` comment block to refer
  to the configured limit instead of the Kubernetes default of 1, and verify no stale "Kubernetes
  default: 1" wording remains

## 4. CHANGELOG for PR #1514

- [x] 4.1 Add an `### Added` entry under `[Unreleased]` covering the `jobDefaults` block, per-job
  override, and the new fsck history limits, and verify it follows the existing entry style
- [x] 4.2 Add a `### Fixed` entry covering `restartPolicy: OnFailure` → `Never` on the three
  remaining job templates (with the log-loss rationale), the two `migration-job.yaml` guard bugs,
  and the inverted "Set to 0 to keep jobs indefinitely" comments
- [x] 4.3 Add a `### Changed` entry recording the N→N+1 attempt-count shift and the
  `failedJobsHistoryLimit` 1→3 change, with the remedy for operators who want the old behavior
- [x] 4.4 Add a `### CI` entry recording that `helm-unittest-check` was a no-op stub and now runs
  the chart suites for real

## 5. Verification

- [x] 5.1 Run `helm unittest charts/ncps` and verify every suite passes including the new scenarios
- [x] 5.2 Render the fsck CronJob with default values and confirm the only difference from the
  previous commit is the two new history-limit fields
- [ ] 5.3 Run `task fmt`, `task lint` and `task test` and verify each exits zero
- [ ] 5.4 Run `nix flake check` and verify it passes
