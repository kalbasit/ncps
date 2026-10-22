## Context

`.github/workflows/releases.yml` runs on `v*.*.*` tag pushes with three jobs: `build`, a reusable
`kalbasit/gh-actions` workflow that builds and pushes Cachix plus multi-arch OCI images; `helm`
(`needs: build`); and `release` (`needs: build`, which drafts the GitHub release). Pre-releases are
recognised by `contains(github.ref, 'alpha'|'beta'|'rc')`. A release commit, for example
`f043b0c9`, is a squash-merged PR with one parent whose only diff inserts
`## [X.Y.Z] - YYYY-MM-DD` under `## [Unreleased]`.

Flake checks live in `nix/checks/flake-module.nix`. CI fans them out automatically from
`nix eval .#checks.<system> --apply builtins.attrNames`, so a new entry needs no workflow wiring.
Small non-Go checks there use `pkgs.stdenvNoCC.mkDerivation` with a narrow `src`
(`openspec-validate-check`, `helm-unittest-check`).

## Goals / Non-Goals

**Goals:**

- One verifier that both the workflow and the flake check run, so CI tests exactly what gates a
  release.
- Fail before *any* publishing job, with a message that says what to do.

**Non-Goals:**

- Blocking tag pushes, or validating changelog prose (see proposal Non-goals).

## Decisions

### D1: A plain bash script, not inline workflow YAML

`.github/scripts/release-tag-guard.sh <tag> [<commit>]` (`<commit>` defaults to `HEAD`) uses only
`bash`, `git` and `grep`, all present on `ubuntu-24.04` runners and easy to provide in the sandbox.
It runs these checks in order and stops at the first failure:

1. Classify the tag. `^v[0-9]+\.[0-9]+\.[0-9]+$` is stable. `^v[0-9]+\.[0-9]+\.[0-9]+-(alpha|beta|rc)`
   is a pre-release: print a notice and exit 0. Anything else fails.
2. Parent count: `git rev-list --parents -n 1 <commit>` must list exactly one parent.
3. Changed files: `git diff --name-only <commit>^ <commit>` must equal exactly `CHANGELOG.md`.
   On failure, print the offending paths.
4. Heading added: `git diff -U0 <commit>^ <commit> -- CHANGELOG.md` must contain an added line
   matching `^\+## \[X\.Y\.Z\]( |$)`, with the version's dots escaped.

Check 4 tests that the heading is *added by this commit*, not just present. On its own it is not
enough: editing an existing heading (for example correcting its date) appears in the diff as a
removed line plus an added `## [X.Y.Z]` line. So a fifth check requires that the parent's
`CHANGELOG.md` does **not** already contain the heading. Together, checks 4 and 5 pin the tag to
the single commit that cut the release. (Raised in review on #1539.)

*Alternative:* inline the logic in a workflow `run:` block. Rejected because it can't be tested
hermetically, and the YAML would drift from whatever we did test.
*Alternative:* treat pre-releases as "anything with a `-suffix`". Rejected because it would let
`v1.2.3-hotfix` skip the guard. Only the three suffixes the workflow already treats as
pre-releases are exempt.

### D2: Workflow wiring

Add a `guard` job with `runs-on: ubuntu-24.04` and `permissions: contents: read`. Its steps:

- `actions/checkout`, pinned to the same SHA the `release` job uses, with `fetch-depth: 2` so the
  parent commit and its `CHANGELOG.md` are available;
- `run: .github/scripts/release-tag-guard.sh "$GITHUB_REF_NAME"`.

Add `needs: guard` to `build`. `helm` and `release` already need `build`, so they skip
transitively. Nothing else in the workflow changes.

### D3: Test suite and flake check

`.github/scripts/release-tag-guard-test.sh <guard-script>` builds a fresh fixture repo under
`mktemp -d` for each scenario. Commit identity comes from `GIT_AUTHOR_*` and `GIT_COMMITTER_*`
environment variables, so no global git config is touched. Each case runs the guard and compares
the exit status (pass or fail) with the expected one. Failure cases also check for a distinctive
substring in the output, so a case can't pass because the script failed for an unrelated reason.
The suite prints `ok`/`FAIL` per case and exits non-zero if any case mismatches. The cases cover
every scenario in `specs/release-tag-guard/spec.md`.

Flake check `release-tag-guard-check` is a `pkgs.stdenvNoCC.mkDerivation` with:

- `src = ../../.github/scripts`;
- `nativeBuildInputs = [ git shellcheck ]` (`bash` comes from stdenv);
- `HOME=$TMPDIR`;
- a build step that runs `shellcheck` on both scripts and then the suite.

It needs no network and no Go.

### D4: TDD

The suite is written test-first, one scenario at a time: add the case, watch it fail against the
current script (or a missing one), then implement just enough. As a last step, run the real
verifier against `f043b0c9`/`v0.10.0` (expect pass) and `dd084152`/`v0.10.1` (expect fail).
This is the real-world check that the original incident would have been caught.

## Risks / Trade-offs

- [A future release commit legitimately touches another file, e.g. bumping a version in
  `charts/ncps/Chart.yaml`] → The guard fails loudly with the offending paths. Either move the
  bump into an earlier PR, or widen the allowlist through a new change. Failing safe beats
  silently publishing.
- [A merge-commit merge strategy produces 2-parent commits] → The repo squash-merges, and the
  guard explains the requirement. A switch to merge commits would need a deliberate change.
- [A re-tag after a failed guard reuses the same version] → Expected. Delete the tag and re-push
  it on the release commit. The guard has no state.

## Migration Plan

Merge this before tagging `v0.10.1` again. The stacked changelog PR's squash commit is the one to
tag. To roll back, revert the workflow `needs:` line.
