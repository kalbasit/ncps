# Capability Spec: Release Tag Guard

## Purpose

Ensures every stable release is cut from a changelog-only release commit, so a published tag
always ships the finalized `CHANGELOG.md` entry for its version and nothing is published from a
mistagged commit.

## Requirements

### Requirement: Stable release tags MUST point at a changelog-only release commit

The release pipeline SHALL verify a stable tag (`vX.Y.Z`) before publishing anything. The tagged
commit MUST have exactly one parent and MUST change no file other than `CHANGELOG.md` compared
with that parent. The commit MUST add a `## [X.Y.Z]` heading to `CHANGELOG.md` whose version
equals the tag without its leading `v`. If any condition fails, the verification SHALL fail with
a message naming the violated condition.

#### Scenario: Changelog-only release commit passes

- **GIVEN** a commit whose only change compared with its single parent is adding `## [1.2.3] - <date>` to `CHANGELOG.md`
- **WHEN** tag `v1.2.3` is verified against it
- **THEN** verification SHALL pass

#### Scenario: Commit touching other files fails

- **GIVEN** a commit that adds `## [1.2.3]` to `CHANGELOG.md` and also modifies another file
- **WHEN** tag `v1.2.3` is verified against it
- **THEN** verification SHALL fail, naming the files other than `CHANGELOG.md`

#### Scenario: Code commit without a changelog change fails

- **GIVEN** a commit that does not modify `CHANGELOG.md`
- **WHEN** tag `v1.2.3` is verified against it
- **THEN** verification SHALL fail

#### Scenario: Heading for a different version fails

- **GIVEN** a changelog-only commit that adds `## [1.2.4]` but not `## [1.2.3]`
- **WHEN** tag `v1.2.3` is verified against it
- **THEN** verification SHALL fail, stating that the `## [1.2.3]` heading is not added

#### Scenario: Heading that already existed in the parent fails

- **GIVEN** a changelog-only commit whose parent's `CHANGELOG.md` already contains `## [1.2.3]`
- **WHEN** tag `v1.2.3` is verified against it
- **THEN** verification SHALL fail, because the commit is not the release commit for `1.2.3`

#### Scenario: Merge or root commit fails

- **GIVEN** a tagged commit with zero parents or with more than one parent
- **WHEN** a stable tag is verified against it
- **THEN** verification SHALL fail, stating that a single-parent commit is required

### Requirement: Pre-release tags MUST be exempt and malformed tags MUST fail

Tags of the form `vX.Y.Z-alpha*`, `vX.Y.Z-beta*`, or `vX.Y.Z-rc*` SHALL pass verification
without any commit-shape or changelog check. Any tag that is neither a stable tag (`vX.Y.Z`)
nor such a pre-release tag SHALL fail verification.

#### Scenario: Release-candidate tag on a code commit passes

- **GIVEN** a commit that changes source files and not `CHANGELOG.md`
- **WHEN** tag `v1.2.3-rc1` is verified against it
- **THEN** verification SHALL pass and report that the tag is a pre-release

#### Scenario: Malformed tag fails

- **WHEN** a tag such as `v1.2` or `v1.2.3-hotfix` is verified
- **THEN** verification SHALL fail, stating that the tag is not a recognised release tag

### Requirement: Nothing MUST be published when tag verification fails

The tag-triggered release workflow SHALL run tag verification as a job that every publishing job
depends on, directly or transitively: binary and OCI image build/push, cache push, Helm chart
publish, and the GitHub release draft. When verification fails, none of those jobs SHALL run.

#### Scenario: Mistagged stable release publishes nothing

- **GIVEN** stable tag `vX.Y.Z` is pushed on a commit that is not a changelog-only release commit
- **WHEN** the release workflow runs
- **THEN** the verification job SHALL fail
- **AND** the build, Helm, and release jobs SHALL be skipped

### Requirement: Tag verification MUST be covered by a hermetic flake check

The `checks` attrset SHALL include a derivation that runs the tag-verification test suite against
fixture git repositories built inside the sandbox, covering every tag-verification scenario of the
two requirements above (the workflow wiring is checked by `actionlint`, not by this derivation). It SHALL fail if
any scenario's verdict differs from the expected one or if the scripts fail shellcheck. It SHALL
NOT require network access or the Go toolchain.

#### Scenario: Regression in the verifier fails CI

- **WHEN** the verifier is changed so that any specified scenario returns the wrong verdict
- **THEN** the flake check SHALL fail
