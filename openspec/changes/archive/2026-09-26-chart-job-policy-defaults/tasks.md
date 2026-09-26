## 1. Make the chart tests runnable and enforced

- [x] 1.1 Replace the separate `kubernetes-helm` + `kubernetes-helmPlugins.helm-unittest` entries in
  `nix/dev-packages.nix` with a `wrapHelm` invocation that registers the plugin, and verify
  `helm unittest charts/ncps` is recognized and reports 9 suites passing from the dev shell
- [x] 1.2 Replace the `helm-unittest-check` no-op stub in `nix/checks/flake-module.nix` with a
  derivation that runs the wrapped helm against the chart, and verify
  `nix build .#checks.x86_64-linux.helm-unittest-check` succeeds and its log shows the suite/test
  counts rather than a "skipped" message
- [x] 1.3 Confirm the check actually gates by temporarily breaking one assertion in
  `charts/ncps/tests/fsck_cronjob_test.yaml`, verifying the check fails, then reverting

## 2. Red — tests for the job policy contract

- [x] 2.1 Add `charts/ncps/tests/job_policy_test.yaml` covering the `restartPolicy: Never` scenarios
  for all four templates, and verify the three migration/chunk-job cases fail against the current
  templates
- [x] 2.2 Extend it with the precedence scenarios (global default applies, per-job override wins,
  one job's override does not leak, overriding one key leaves others defaulted) and verify they fail
  because `jobDefaults` does not exist yet
- [x] 2.3 Add the omit-on-null scenarios for `backoffLimit` and `ttlSecondsAfterFinished`, including
  an assertion that the rendered output contains no `<no value>`, and verify the migration-job
  `backoffLimit` case fails on the current unguarded template
- [x] 2.4 Add the explicit-zero scenarios for both keys and verify the migration-job
  `ttlSecondsAfterFinished: 0` case fails on the current truthiness guard
- [x] 2.5 Add the default-render regression scenarios pinning `backoffLimit`/`ttlSecondsAfterFinished`
  to today's values for all four jobs, and verify they pass before any template change (they encode
  current behavior and must stay green throughout)

## 3. Green — implement the resolution

- [x] 3.1 Add `jobDefaults` to `values.yaml` with `restartPolicy: Never`, `backoffLimit: 1`,
  `ttlSecondsAfterFinished: 3600`, documenting the precedence rules and the
  `jobDefaults.<key>: null` remedy for per-job omission, and verify `helm template` still renders
- [x] 3.2 Add the `ncps.job.restartPolicy`, `ncps.job.backoffLimit` and
  `ncps.job.ttlSecondsAfterFinished` helpers to `_helpers.tpl` using `kindIs "invalid"` (never
  `coalesce` or truthiness, per design), and verify with a scratch `helm template` that an explicit
  `0` survives resolution
- [x] 3.3 Convert `fsck-cronjob.yaml` to the helpers and verify the fsck scenarios from group 2 pass
- [x] 3.4 Convert `migration-job.yaml` to the helpers, replacing both broken guards, and verify its
  `restartPolicy`, omit-on-null, and explicit-zero scenarios pass
- [x] 3.5 Convert `migrate-chunks-to-nar-job.yaml` and `migrate-nar-to-chunks-job.yaml` to the
  helpers and verify their scenarios pass
- [x] 3.6 Set the per-job `backoffLimit`/`ttlSecondsAfterFinished` in `values.yaml` to `null` where
  the job takes the global default, keeping `migration.job`'s explicit `3`/`300`, and verify the
  group 2.5 default-render regression scenarios are still green

## 4. Schema and documentation

- [x] 4.1 Widen `backoffLimit`/`ttlSecondsAfterFinished` to `["integer", "null"]` in
  `values.schema.json`, add the `jobDefaults` object, and add the missing
  `migrateChunksToNar`/`migrateNarToChunks` job blocks; verify `helm lint charts/ncps` passes with
  those keys set to null
- [x] 4.2 Correct the four "Set to 0 to keep jobs indefinitely" comments in `values.yaml` to state
  that `0` deletes immediately and `null` retains indefinitely, and verify no occurrence of the old
  wording remains
- [x] 4.3 Document the `OnFailure` to `Never` attempt-count shift (N to N+1 complete attempts) in
  the `jobDefaults` comment block, and update `charts/ncps/README.md` if it documents these values

## 5. Verification

- [x] 5.1 Run `helm unittest charts/ncps` and verify every suite passes with the new scenarios
  included
- [x] 5.2 Render each job with default values and diff the `backoffLimit`/`ttlSecondsAfterFinished`
  lines against the same render on `main`, verifying only `restartPolicy` differs
- [x] 5.3 Run `task fmt`, `task lint` and `task test` and verify each exits zero
- [ ] 5.4 Run `nix flake check` and verify it passes with `helm-unittest-check` now executing for
  real
