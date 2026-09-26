## ADDED Requirements

### Requirement: `helm-unittest-check` executes the chart's unit tests

The `helm-unittest-check` derivation MUST actually run the chart's `helm unittest` suites and fail
when any of them fail. It MUST NOT pass unconditionally as a skip stub: a check that always succeeds
provides no regression protection for the chart templates while appearing green in the gate.

The plugin is supplied by wrapping the chart-testing `helm` with the `helm-unittest` plugin from the
flake's pinned nixpkgs, so the check runs hermetically and offline with no plugin installation at
build time. The same wrapped `helm` MUST be available in the development shell, so that
`helm unittest charts/ncps` reproduces the CI result locally.

#### Scenario: A failing chart assertion fails the gate

- **WHEN** a chart template is changed so that an existing assertion in `charts/ncps/tests/` no
  longer holds
- **THEN** `helm-unittest-check` fails
- **AND** `nix flake check` fails

#### Scenario: The check reports the suites it ran

- **WHEN** `helm-unittest-check` succeeds
- **THEN** its build log reports the number of test suites and tests executed, rather than a
  "skipped" message

#### Scenario: The dev shell can run the same tests

- **WHEN** a developer runs `helm unittest charts/ncps` from the project's development shell
- **THEN** the command is recognized and executes the chart's test suites
