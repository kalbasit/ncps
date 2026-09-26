## Context

See `proposal.md` — Why. This is the last piece of PR #1514, which introduced the `helm-job-policy`
capability. Constraints that shape the approach:

- `fsck-cronjob.yaml` is the only CronJob the chart renders; the other three job templates are plain
  Jobs, for which `failedJobsHistoryLimit`/`successfulJobsHistoryLimit` do not exist.
- The chart already has an established home for CronJob-only knobs: `fsck.job.concurrencyPolicy`.
- The `jobDefaults` block and the `ncps.job.*` helpers added by the previous change resolve keys
  that apply to every job. History limits are not such keys.
- Kubernetes defaults, confirmed in `pkg/apis/batch/v1/defaults.go`:
  `SuccessfulJobsHistoryLimit=3`, `FailedJobsHistoryLimit=1`.

## Goals / Non-Goals

**Goals:**

- A failed fsck run is not destroyed by the next failure.
- Both history limits are tunable from values, including "omit and take the Kubernetes default".
- The interaction between TTL and history limits is documented where operators will hit it.

**Non-Goals:**

- Adding history limits to `jobDefaults` (see Decisions).
- Changing the `ttlSecondsAfterFinished` default (see Decisions).
- Adding history limits to the three plain Jobs — the fields do not exist on `batch/v1` `Job`.

## Decisions

### Put the limits on `fsck.job`, not in `jobDefaults`

`jobDefaults` exists to give one value to every job the chart renders. `failedJobsHistoryLimit` and
`successfulJobsHistoryLimit` are fields on `CronJobSpec`, not `JobSpec`, so a `jobDefaults` entry
would silently do nothing for three of the four jobs. That is precisely the reasoning the previous
change recorded when it excluded `concurrencyPolicy` from `jobDefaults`, and these keys sit next to
it for the same reason.

*Alternative considered:* a `jobDefaults.cronJob.*` sub-block, to keep all policy in one place.
Rejected — one CronJob does not justify a second nesting level, and the per-job block is where an
operator configuring fsck is already looking.

Consequently these keys do **not** go through the `ncps.job.*` helpers. Those helpers implement
per-job/global precedence, which does not apply here; the template guards them directly with
`kindIs "invalid"`, the same null test the helpers use, so an explicit `0` still renders.

### Default `failedJobsHistoryLimit` to 3, accepting the behavior change

Kubernetes defaults this to 1, which means one failure erases the previous failure's pod. Since the
whole point of `restartPolicy: Never` in this capability is that a failed run stays inspectable, 1
is the wrong default for fsck. Three gives an operator a short history to compare against — enough
to tell a recurring failure from a one-off — without unbounded accumulation.

`successfulJobsHistoryLimit` is set to 3, which is exactly the Kubernetes default. It changes
nothing today; it is declared so both limits are visible and tunable in one place rather than one
being configurable and its twin invisible.

### Do not change the TTL default, document the composition instead

`ttlSecondsAfterFinished` and the history limits are independent, and whichever fires first wins.
fsck inherits `jobDefaults.ttlSecondsAfterFinished: 3600`, so in the shipped configuration a failed
fsck Job is deleted an hour after it finishes and the new history limit is not the binding
constraint. Raising the history limit alone therefore does not, by itself, extend retention.

The TTL default is deliberately left alone. TTL cannot distinguish a failed run from a successful
one, so raising it to lengthen failure retention would also retain every successful nightly run,
which is not what an operator asked for. The honest fix is to document the composition — in
`values.yaml`, the Chart Reference and the spec — and let the operator clear the TTL at both levels
when they want the history limit to govern. The new knob is still worth shipping: it is what makes
that configuration possible at all, and it removes the silent `1`.

## Risks / Trade-offs

- **Retaining three failed Jobs costs cluster objects and disk** → bounded and small: at most three
  Job objects and their pods, and the inherited 1h TTL still clears them in the default
  configuration. Operators wanting the old behavior set `fsck.job.failedJobsHistoryLimit: 1`.
- **Shipping a knob that the default TTL neutralizes could read as ineffective** → mitigated by
  documenting the interaction prominently rather than burying it; the alternative (silently raising
  the TTL) would change successful-run retention as a side effect.
- **Two more values to keep in sync across values.yaml, the schema, and the docs** → the new helm
  unittest scenarios cover the rendered output, and `helm-unittest-check` now actually runs in CI,
  so drift fails the gate.

## Migration Plan

None required — a template and values change with no state.

- Operators who set nothing get `failedJobsHistoryLimit: 3` (previously an implicit 1) and
  `successfulJobsHistoryLimit: 3` (previously an implicit 3, unchanged).
- To restore the previous failed-run behavior exactly, set `fsck.job.failedJobsHistoryLimit: 1`;
  to go back to letting Kubernetes decide, set it to `null`.
- Rollback is a chart version pin.
