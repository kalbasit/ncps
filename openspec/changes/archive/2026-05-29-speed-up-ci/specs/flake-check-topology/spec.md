# flake-check-topology (delta: speed-up-ci)

## ADDED Requirements

### Requirement: Integration cohorts validated on a single CI architecture

CI SHALL validate the integration test suite (`nix flake check`, i.e. the
backend cohorts and coverage) on exactly one canonical architecture
(`x86_64-linux`). Non-canonical architectures in the build matrix
(`aarch64-linux`) MUST NOT run `nix flake check` or build coverage in CI; they
MUST still build their deployable OCI image so the multi-arch manifest remains
complete. This is configured by the caller passing
`test_systems: '["x86_64-linux"]'` to the shared `kalbasit/gh-actions` workflow.

This narrows where the suite runs, not what it covers: the cohorts, backends,
race detector, and the single merged Codecov profile are unchanged from the
`Quality invariants preserved across the topology change` requirement, because
those all derive from the canonical (x86_64) run.

#### Scenario: aarch64 CI leg skips the integration suite

- **WHEN** CI runs for a pull request with the default
  `systems: '["x86_64-linux","aarch64-linux"]'` and
  `test_systems: '["x86_64-linux"]'`
- **THEN** the `aarch64-linux` build leg SHALL NOT execute `nix flake check` or
  build/upload coverage
- **AND** the `aarch64-linux` leg SHALL still run
  `nix build .#packages.aarch64-linux.docker`

#### Scenario: x86_64 CI leg runs the full suite

- **WHEN** CI runs for the same pull request
- **THEN** the `x86_64-linux` build leg SHALL run `nix flake check -L` across all
  backend cohorts and produce the single merged coverage profile uploaded to
  Codecov

#### Scenario: Local flake check is unaffected

- **WHEN** a developer runs `nix flake check` locally on any system
- **THEN** all checks for that system SHALL run as before; the `test_systems`
  scoping is a CI-only workflow input and does not change flake behavior
