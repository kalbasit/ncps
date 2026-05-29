# Tasks: speed-up-ci

Cross-repo change. Land the `kalbasit/gh-actions` tasks first (the new input
must exist before ncps can pass it), then ncps, then verify.

## 1. gh-actions: add opt-in `test_systems` (land first)

Repo: `../gh-actions` (relative to this repo's parent in your workspace).

- [x] 1.1 Scaffold a sibling OpenSpec change in gh-actions:
  `scope-flake-check-systems`.
- [x] 1.2 `build.yml`: add a `test_systems` input (JSON-array string, default
  `"[]"`). Document: empty ⇒ all `systems`; non-empty ⇒ flake-check/coverage
  only on listed systems.
- [x] 1.3 `build.yml`: gate the **Flake check**, **Build coverage**, and
  **Upload coverage to Codecov** steps with
  `if: inputs.test_systems == '[]' || contains(fromJson(inputs.test_systems), matrix.system)`
  (preserve the existing non-fork condition on coverage/codecov by ANDing).
- [x] 1.4 `build.yml`: confirm **Build the OCI image** (and pusher/login/push)
  and the `oci-manifest` job remain ungated (run on every system).
- [x] 1.5 `ci.yml` (orchestrator): add a `test_systems` input and forward it to
  the `build` job; apply the same gating to the standalone `flake-check` job
  (used when `oci: false`).
- [x] 1.6 Update spec `openspec/specs/oci-build/spec.md` delta: MODIFY
  `Reusable OCI build workflow` so flake-check/coverage are scoped to
  `test_systems`; image build/push stays per-arch. Update the two scenarios
  accordingly.
- [x] 1.7 Update spec `openspec/specs/reusable-ci/spec.md` delta for the new
  forwarded `test_systems` input.
- [x] 1.8 `openspec validate` (gh-actions) passes; `_lint-workflows.yml` /
  actionlint clean.
- [x] 1.9 `/git-commit` on a feature branch in gh-actions; open a PR.
  → kalbasit/gh-actions#8

## 2. ncps: opt in

- [x] 2.1 `.github/workflows/ci.yml`: in the `shared` job `with:`, add
  `test_systems: '["x86_64-linux"]'`.
- [x] 2.2 Keep `systems` default (both arches) so the aarch64 image still builds.
- [x] 2.3 Spec delta `flake-check-topology` (already written) reflects single-arch
  CI validation; input name `test_systems` matches the gh-actions PR.
- [x] 2.4 `openspec validate --change speed-up-ci` passes.
- [x] 2.5 Commit on the feature branch; open a PR. → kalbasit/ncps#1291
- [x] 2.6 TEMP: repoint the `shared` caller from `@main` to the gh-actions
  feature branch (`@user/wnasreddine/ncps-ci-got-slow-again`) so this PR's CI
  validates `test_systems` end-to-end before kalbasit/gh-actions#8 merges.
- [x] 2.7 **REVERT** the caller back to `@main` (done after end-to-end CI
  validation on the temp branch pin; gh-actions#8 merges first).

## 3. Verify

> ncps#1291 temporarily pins the `shared` caller to the gh-actions feature
> branch (task 2.6), so CI can validate these now — before gh-actions#8 merges.
> After verifying, revert to `@main` (task 2.7); merge gh-actions#8 first, then ncps#1291.

- [x] 3.1 aarch64 leg skips the suite, builds docker only. → run 26659554500:
  `build (aarch64-linux)` **success in 2.7m** (was 25.7m + failing).
- [x] 3.2 x86_64 leg runs the full cohort suite. → `build (x86_64-linux)`
  **success in 17.2m**.
- [x] 3.3 Multi-arch manifest still assembles. → `oci-manifest` **success in 0.4m**.
- [x] 3.4 Before/after: aarch64 leg 25.7m(fail, Postgres timeout) → 2.7m(pass);
  total run ~26m(fail) → 17m(pass). Flaky failure eliminated. (x86 leg at 17.2m
  is the remaining bottleneck — the deferred cohort-split lever.)
- [x] 3.5 `task fmt` clean; no Go changed (workflow/docs only); actionlint +
  openspec validate green. The only red job is `openspec-guard` (expected: the
  change is archived as the final pre-merge step).
