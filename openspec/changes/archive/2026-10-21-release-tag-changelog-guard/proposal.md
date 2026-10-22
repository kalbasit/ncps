## Why

A release tag is supposed to point at the "release" commit: the squash-merged PR that only moves
the `[Unreleased]` entries under a new `## [X.Y.Z] - date` heading in `CHANGELOG.md` (e.g.
`f043b0c9 docs(changelog): release v0.10.0`). Nothing checks this. `v0.10.1` was first pushed on a
code commit, so its tarball had no `0.10.1` changelog entry. The tag had to be deleted and the
`Releases` run cancelled by hand. By then that run had already started publishing Docker images and
Cachix artifacts from the wrong commit.

## What Changes

- Add a release-tag guard script. Given a tag and the checked-out commit, it passes only if both
  of these hold:
  - the commit changes **only** `CHANGELOG.md` compared with its single parent;
  - the commit **adds** a `## [X.Y.Z]` heading whose version matches the tag (without its `v`).
- Pre-release tags (`vX.Y.Z-alpha*`, `-beta*`, `-rc*`) are exempt, matching how the workflow
  already classifies pre-releases. Malformed tags fail.
- Add a `guard` job to `.github/workflows/releases.yml` that runs the script. Make `build` depend
  on it (`helm` and `release` already depend on `build`), so a bad tag publishes nothing.
- Add a hermetic flake check (`release-tag-guard-check`) that runs the script's test suite against
  fixture git repositories and shellchecks the scripts. CI picks the check up automatically.

## Non-goals

- Preventing a bad tag from being *pushed*. GitHub tag rulesets cannot inspect commit contents,
  so this change only stops the release pipeline. A local pre-push hook is out of scope.
- Validating changelog *content* beyond the version heading (dates, section names, wording).
- Changing how pre-releases are built or published.
- Re-tagging `v0.10.1`. The changelog commit to tag ships as a separate stacked PR.

## Capabilities

### New Capabilities

- `release-tag-guard`: the release pipeline refuses to publish a non-pre-release tag unless the
  tagged commit is a changelog-only release commit that adds the matching version heading.

### Modified Capabilities

_None._ `flake-check-topology` already derives CI jobs from the live `checks` enumeration, so
the new check needs no requirement change there.

## Impact

- **Code/CI**: new `.github/scripts/release-tag-guard.sh` and `.github/scripts/release-tag-guard-test.sh`;
  `.github/workflows/releases.yml` (new `guard` job, `build.needs`); `nix/checks/flake-module.nix`
  (new check).
- **Runtime I/O / network latency / memory**: none. ncps itself is untouched. A release run gains
  one short job (a 2-commit checkout plus `git diff`) ahead of `build`.
- **Process**: maintainers must tag the changelog release commit. Tagging anything else now fails
  fast in the `guard` job, before any artifact is published.
