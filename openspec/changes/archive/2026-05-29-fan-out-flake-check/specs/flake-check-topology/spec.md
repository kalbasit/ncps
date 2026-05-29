# flake-check-topology (delta: fan-out-flake-check)

## ADDED Requirements

### Requirement: CI runs each check as an independent parallel job

CI SHALL execute the flake's checks by fanning them out — running each
`checks.<system>.<name>` derivation as its own parallel job
(`nix build .#checks.<system>.<name>`) — rather than as a single monolithic
`nix flake check` on one runner. The set of jobs MUST be derived from the live
checks enumeration (`nix eval .#checks.<system> --apply builtins.attrNames`), so
adding or removing a check automatically adds or removes its job with no further
wiring. A single check's failure MUST NOT cancel the others (`fail-fast: false`),
and the CI gate MUST fail if any check job fails.

The checks run on the canonical CI architecture (`x86_64-linux`, per the
`Integration cohorts validated on a single CI architecture` requirement). Each
job relies on the shared build cache from Cachix so it pulls the compiled cohort
artifacts rather than rebuilding them.

#### Scenario: Each check is its own job

- **WHEN** CI runs for a pull request
- **THEN** for each attribute in `checks.x86_64-linux` there SHALL be a
  corresponding job running `nix build .#checks.x86_64-linux.<name>`
- **AND** the jobs SHALL run in parallel on separate runners
- **AND** one check failing SHALL NOT cancel the other check jobs, but SHALL fail
  the overall CI gate

#### Scenario: Check list is derived, not hand-maintained

- **WHEN** a check is added to or removed from the flake's `checks` attrset
- **THEN** its CI job SHALL appear or disappear automatically, because the matrix
  is computed from `nix eval .#checks.x86_64-linux --apply builtins.attrNames`

### Requirement: Coverage is produced by a job consuming the fanned-out cohorts

The single merged Codecov profile SHALL be produced by a dedicated CI job that
runs after the check jobs and builds `nix build .#ncps.coverage` (which merges
the per-cohort `cover.out` outputs), then uploads it to Codecov. Coverage upload
is informational and MUST NOT gate the build; a transient cache miss MAY cause a
local rebuild but MUST NOT fail CI.

#### Scenario: Coverage merged after checks

- **WHEN** the check jobs have completed
- **THEN** a coverage job SHALL build `.#ncps.coverage` and upload exactly one
  merged profile to Codecov
- **AND** a failure to upload coverage SHALL NOT fail the CI gate
