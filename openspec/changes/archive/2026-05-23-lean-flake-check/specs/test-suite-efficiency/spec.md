## ADDED Requirements

### Requirement: Profiling at Nix-check-derivation granularity

The profiling workflow defined by this capability SHALL additionally
produce per-derivation wall-clock figures for each entry in the
`checks` attribute set of the project's flake, captured on
CI-equivalent hardware on a cold Nix cache. Per-test profiling alone
is insufficient when the test-suite wall-clock cost is dominated by
service spin-up and Nix-level serialization rather than by Go test
work.

#### Scenario: Capturing per-derivation timings

- **WHEN** a developer runs the profiling workflow with the
  `--check-derivations` mode (or equivalent) on a cold Nix store
- **THEN** the workflow produces a ranked list of every member of
  `nix flake show --json | jq '.checks'` with its wall-clock build
  time
- **AND** the output identifies which derivations started which
  backend services
- **AND** the output is written to a deterministic file under
  `openspec/changes/<change>/` for diffing across phases

#### Scenario: Optimizing wall-clock without per-derivation data

- **WHEN** a change proposes to reduce `nix flake check` wall-clock
  by more than 10% by restructuring the check graph (as opposed to
  shrinking individual tests)
- **THEN** the change MUST include a baseline per-derivation timings
  file
- **AND** a post-change per-derivation timings file showing the
  improvement
- **AND** the improvement MUST be reproducible — re-running the
  workflow on the same hardware yields figures within a documented
  tolerance band
