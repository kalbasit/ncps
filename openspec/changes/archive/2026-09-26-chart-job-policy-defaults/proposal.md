## Why

Three of the chart's four Job/CronJob templates still set `restartPolicy: OnFailure`, which makes
the Job controller delete the failing pod the moment the backoff limit is reached — taking the
failing run's logs with it. PR #1510 fixed this for `fsck-cronjob.yaml` only. At the same time the
retry and cleanup knobs (`backoffLimit`, `ttlSecondsAfterFinished`) are duplicated per job with no
shared default and inconsistent null-handling, and `restartPolicy` is not configurable at all, so
operators must file a PR against the chart to change any of it.

Separately, the `helm-unittest-check` flake check is a no-op stub, so none of the chart's 171 unit
tests — including the assertion PR #1510 just added — actually guard anything in CI.

## What Changes

- Set `restartPolicy` to `Never` for the three remaining job templates (`migration-job.yaml`,
  `migrate-chunks-to-nar-job.yaml`, `migrate-nar-to-chunks-job.yaml`), so a failed run's pod is
  retained for inspection instead of being deleted at the backoff limit.
- Add a top-level `jobDefaults` block in `values.yaml` supplying `restartPolicy`, `backoffLimit`
  and `ttlSecondsAfterFinished` for every job, with each per-job block
  (`migration.job`, `fsck.job`, `migrateChunksToNar.job`, `migrateNarToChunks.job`) able to
  override any of the three independently. Resolution order is per-job value, then `jobDefaults`
  value, then omit the field entirely.
- Route all four templates through one shared `_helpers.tpl` helper so the resolution rules cannot
  drift between them.
- Fix `migration-job.yaml`'s two guard bugs: `backoffLimit` is unguarded, so a `null` renders the
  literal `backoffLimit: <no value>`; `ttlSecondsAfterFinished` uses a truthiness guard, so an
  explicit `0` is silently dropped. The other three templates already use the `kindIs "invalid"`
  form.
- Correct the `values.yaml` comment "Set to 0 to keep jobs indefinitely" (4 occurrences). Kubernetes
  TTL-after-finished semantics are the opposite: `0` deletes the job immediately once it finishes,
  and omitting the field is what keeps it indefinitely.
- Replace the `helm-unittest-check` no-op stub with a real check that runs `helm unittest` against
  the chart, and expose the same plugin-wrapped `helm` in the dev shell.

**Not breaking**, but two behavioral notes are recorded in `design.md`: the default rendered output
is unchanged, while the `OnFailure` to `Never` switch does shift how many complete attempts each job
makes, because the Job controller compares the backoff limit differently per policy.

## Capabilities

### New Capabilities

- `helm-job-policy`: how the chart resolves `restartPolicy`, `backoffLimit` and
  `ttlSecondsAfterFinished` for every Job and CronJob it renders — the global-default/per-job
  override precedence, the omit-on-null behavior, and the requirement that failed job pods are
  retained for debugging.

### Modified Capabilities

- `flake-check-topology`: `helm-unittest-check` currently passes unconditionally as a documented
  skip. It becomes a real check that executes the chart's unit tests and fails the gate when they
  fail.

## Impact

- `charts/ncps/templates/_helpers.tpl` — new shared job-policy helper.
- `charts/ncps/templates/migration-job.yaml`, `migrate-chunks-to-nar-job.yaml`,
  `migrate-nar-to-chunks-job.yaml`, `fsck-cronjob.yaml` — consume the helper.
- `charts/ncps/values.yaml` — new `jobDefaults` block; per-job knobs default to `null`; corrected
  TTL comments.
- `charts/ncps/values.schema.json` — schema entries for the new and nulled values.
- `charts/ncps/tests/*.yaml` — unit tests for the resolution matrix.
- `nix/checks/flake-module.nix` — real `helm-unittest-check`.
- `nix/devshell` — plugin-wrapped `helm` so `helm unittest charts/ncps` works locally.
- No Go code, database, or runtime behavior is touched.
