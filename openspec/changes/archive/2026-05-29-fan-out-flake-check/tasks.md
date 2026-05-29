# Tasks: fan-out-flake-check

Land the gh-actions input first, then ncps, then verify.

## 1. gh-actions: `run_flake_check` opt-in (land first)

Repo: `../gh-actions` (branch already created).

- [x] 1.1 Scaffolded `skip-monolithic-flake-check` in gh-actions.
- [x] 1.2 `build.yml`: added `run_flake_check` input (default `true`); gated the
  Flake check / Build coverage / Codecov steps on it.
- [x] 1.3 `ci.yml` orchestrator: added `run_flake_check`; forwarded to
  `build.yml`; gated the standalone `flake-check` job.
- [x] 1.4 Spec deltas synced: `oci-build` + `reusable-ci`.
- [x] 1.5 `openspec validate` + `actionlint` clean; committed; PR
  kalbasit/gh-actions#9.

## 2. ncps: fan-out matrix + coverage

- [x] 2.1 `.github/workflows/ci.yml`: `shared` caller now passes
  `run_flake_check: false`.
- [x] 2.2 Added `check-matrix` job (nix eval → `outputs.checks`).
- [x] 2.3 Added `checks` job (`fail-fast: false` matrix over the check list;
  `nix build .#checks.x86_64-linux.<check> -L`).
- [x] 2.4 Added `coverage` job (`needs: checks`, non-fork; `nix build
  .#ncps.coverage` + `codecov-action@v6`, `files: result-coverage`).
- [x] 2.5 Final `ci` gate now `needs` check-matrix/checks/coverage; `skipped`
  (fork) treated as pass.
- [x] 2.6 `shared` build job no longer runs flake-check (run_flake_check:false).
- [x] 2.7 TEMP: pinned the `shared` caller to the gh-actions feature branch to
  validate end-to-end before kalbasit/gh-actions#9 merges.
- [x] 2.8 **REVERT** the caller to `@main` (done after gh-actions#9 merged).

## 3. Verify

- [ ] 3.1 `nix eval .#checks.x86_64-linux --apply builtins.attrNames --json`
  returns the expected check list locally.
- [ ] 3.2 `actionlint` clean on ncps `ci.yml`.
- [ ] 3.3 On the PR: each check is its own parallel job; capture per-job times
  (this IS the per-check floor measurement, now free); confirm wall-clock drops
  toward ~3–4m.
- [ ] 3.4 Coverage job uploads one merged profile; OCI manifest still assembles.
- [ ] 3.5 If per-job times show wasteful setup on the trivially-fast checks,
  note a follow-up to group them (do not block on it).
