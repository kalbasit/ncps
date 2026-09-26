## Context

See `proposal.md` — Why. Current state that shapes the approach:

- Four templates render a Job or CronJob pod spec: `migration-job.yaml`, `fsck-cronjob.yaml`,
  `migrate-chunks-to-nar-job.yaml`, `migrate-nar-to-chunks-job.yaml`.
- `restartPolicy` is a hardcoded literal in each. PR #1510 changed `fsck-cronjob.yaml` to `Never`;
  the other three are still `OnFailure`.
- `backoffLimit`/`ttlSecondsAfterFinished` are already per-job values, but each template repeats its
  own guard logic, and `migration-job.yaml` disagrees with the other three: its `backoffLimit` has
  no guard at all (a null renders `backoffLimit: <no value>`, which is invalid YAML for an integer
  field) and its `ttlSecondsAfterFinished` uses a truthiness guard (`{{- if .Values... }}`), which
  silently drops an explicit `0`.
- `charts/ncps/values.schema.json` types both keys as `{"type": "integer"}`, so a null value fails
  schema validation today.
- `nix/dev-packages.nix:20-21` lists `kubernetes-helm` and `kubernetes-helmPlugins.helm-unittest` as
  two independent packages. Putting a helm plugin on `PATH` does not register it with helm — helm
  discovers plugins through `HELM_PLUGINS` — which is why `helm unittest` reports
  `unknown command "unittest"` in the dev shell, and why `helm-unittest-check` was stubbed out.

## Goals / Non-Goals

**Goals:**

- One resolution path for job policy, shared by all four templates, so the guard inconsistency
  cannot reappear.
- Operators can change retry/cleanup/restart behavior for all jobs at once, or for one job, from
  values alone.
- Default rendered output for `backoffLimit`/`ttlSecondsAfterFinished` is byte-identical to today.
- `helm-unittest-check` becomes a real gate so the chart tests — including #1510's — have teeth.

**Non-Goals:**

- Changing the shipped numeric defaults to compensate for the attempt-count shift (see Decisions).
- Folding `concurrencyPolicy` into `jobDefaults`: it is a CronJob-only field and only `fsck` renders
  a CronJob, so a global default would be misleading.
- Folding the per-job `annotations`/`nodeSelector`/`tolerations`/`affinity` knobs into
  `jobDefaults`. They are already per-job and are not part of the retry/cleanup problem; widening
  the blast radius of this change buys nothing.
- Any change to Go code, the database, or runtime behavior.

## Decisions

### Resolve policy in one `_helpers.tpl` helper, not per template

A single helper per key (`ncps.job.backoffLimit`, `ncps.job.ttlSecondsAfterFinished`,
`ncps.job.restartPolicy`) takes the per-job `job` dict and the root context, and emits either the
resolved line or nothing at all. Each template calls the helper instead of writing its own guard.

*Alternative considered:* repeat a `kindIs "invalid"` ladder in each of the four templates. Rejected
— that is exactly how `migration-job.yaml` drifted from the other three in the first place, and this
change would go from one divergence to three more opportunities for one.

### Use `kindIs "invalid"` for the null test, never `coalesce` or truthiness

Sprig's `coalesce` and Go template truthiness both treat `0` as empty, so `coalesce .job.backoffLimit
.Values.jobDefaults.backoffLimit` would silently discard a deliberate `backoffLimit: 0` and fall
through to the global default. Since `0` is meaningful for both keys (no retries; delete immediately
once finished), the only correct null test is `kindIs "invalid"`, which distinguishes "not set" from
"set to zero".

This is the root cause of the existing `migration-job.yaml` TTL bug, so the helper is also the fix
for it.

### Per-job null means "inherit"; null at both levels means "omit"

Precedence is: per-job value if non-null, else `jobDefaults` value if non-null, else omit the key
entirely so the Kubernetes default applies. Per-job keys ship as `null` in `values.yaml`, and the
real numbers live in `jobDefaults`, except where a job needs a different value from the global one
(the migration job keeps explicit `backoffLimit: 3` and `ttlSecondsAfterFinished: 300`).

*Trade-off:* "omit this key for exactly one job while the others keep the global value" is no longer
directly expressible, because a per-job null now means inherit. An operator who needs it sets
`jobDefaults.<key>: null` and gives the other jobs explicit values. This is documented in
`values.yaml`. A sentinel value (e.g. the string `"none"`) was considered to preserve per-job opt-out
and rejected: it makes the common case harder to read and pushes a non-integer into an integer
field, for a case no current user has.

`values.schema.json` therefore widens both keys from `"integer"` to `["integer", "null"]`, and gains
a `jobDefaults` object. The `migrateChunksToNar`/`migrateNarToChunks` blocks are absent from the
schema entirely today; they are added so all four jobs validate consistently.

### Keep the numeric defaults and document the attempt-count shift

Switching `OnFailure` to `Never` changes how many complete attempts a job makes, because the Job
controller compares the backoff limit differently per policy:

- `pkg/controller/job/job_controller.go:1558`, OnFailure: `restartCountSum >= backoffLimit`
- `pkg/controller/job/job_controller.go:918`, Never: `failedPods > backoffLimit`

So at `backoffLimit: N`, `OnFailure` yields N complete attempts (the N+1th container is started and
then torn down almost immediately when the controller notices the limit), while `Never` yields N+1
complete attempts. Concretely, the migration job goes from 3 complete attempts to 4, and the two
migrate-* jobs from 1 to 2.

The defaults are left alone rather than decremented to compensate. Rationale: the same shift was
accepted for `fsck` in #1510, `backoffLimit: N` now means the plainly documented "N retries after
the first attempt", and silently decrementing the shipped values to preserve an artifact of the old
policy would be harder to explain than the shift itself. The shift is recorded in the chart
`values.yaml` comments and in the PR body. Operators who want the old effective count can now set it
themselves — which is the point of the change.

### Wrap helm with the plugin rather than installing it at build time

`pkgs.wrapHelm pkgs.kubernetes-helm { plugins = [ pkgs.kubernetes-helmPlugins.helm-unittest ]; }`
produces a helm whose `HELM_PLUGINS` already contains the plugin, so the check needs no network and
no writable plugin directory in the sandbox. Verified against this flake's pinned nixpkgs: it builds
and runs all 9 suites / 171 tests green, which contradicts the stub's claim that the plugin binaries
are missing on this platform.

*Alternative considered:* `helm plugin install` in the derivation's build phase. Rejected — it needs
network access, which the nix sandbox denies.

## Risks / Trade-offs

- **Un-stubbing `helm-unittest-check` turns a green check red if any chart test is currently
  failing** → all 9 suites / 171 tests were run green before writing this design, so the check goes
  live already passing. It is also added to the same gate that already runs the other chart checks,
  so a failure is attributable.
- **The attempt-count shift doubles the worst-case runtime of a failing job** → for `fsck` this is
  the significant one (a measured production run took 3h at ~4.8 GiB), and it was already accepted
  in #1510. Daily scheduling with `concurrencyPolicy: Forbid` leaves ample headroom, and operators
  can now lower `backoffLimit` from values without a chart release.
- **Retaining failed pods consumes cluster resources** → bounded by `ttlSecondsAfterFinished`, which
  stays at its current value for every job, so the retention window is unchanged from today.
- **Widening the schema to accept null loses a little validation strictness** → the omit-on-null
  path is what makes null meaningful, and the templates guard the rendering, so an invalid manifest
  cannot result.
- **The per-job "omit" case becomes indirect** → documented in `values.yaml` and in the spec; see
  the precedence decision above.

## Migration Plan

No migration is required: this is a template/values change with no state and no runtime component.

- Operators who set nothing get identical `backoffLimit`/`ttlSecondsAfterFinished` output and the
  `restartPolicy` change.
- Operators who currently set `<job>.job.backoffLimit` or `<job>.job.ttlSecondsAfterFinished`
  explicitly keep working unchanged — a per-job value still wins.
- Operators who currently set one of those keys to `null` to omit the field are the one affected
  group: they will now inherit the `jobDefaults` value instead of getting the field omitted. The
  release note calls this out with the `jobDefaults.<key>: null` remedy.
- Rollback is a chart version pin; nothing outside the rendered manifests changes.
