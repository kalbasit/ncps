# Tasks: speed-up-ci

Cross-repo change. Land the `kalbasit/gh-actions` tasks first (the new input
must exist before ncps can pass it), then ncps, then verify.

## 1. gh-actions: add opt-in `test_systems` (land first)

Repo: `../gh-actions` (`/home/wnasreddine/.../github.com/kalbasit/gh-actions`).

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

## 3. Verify

> Blocked on merge order: kalbasit/gh-actions#8 must land before kalbasit/ncps#1291's
> CI exercises the new input. These are observed on the ncps PR's CI run after #8 merges.

- [ ] 3.1 On the ncps PR, confirm the `build (aarch64-linux)` leg does **not**
  run `nix flake check` / coverage (check the job log) but **does** run the
  docker build.
- [ ] 3.2 Confirm the `build (x86_64-linux)` leg runs the full cohort suite +
  Codecov upload (one merged profile).
- [ ] 3.3 Confirm the multi-arch manifest (`oci-manifest`) still assembles both
  arch images on a push/tag build.
- [ ] 3.4 Record before/after CI wall-clock; confirm the aarch64 Postgres-timeout
  failure no longer gates merges and total time drops materially.
- [ ] 3.5 `task fmt`, `task lint`, `task test` green in ncps before reporting done.
