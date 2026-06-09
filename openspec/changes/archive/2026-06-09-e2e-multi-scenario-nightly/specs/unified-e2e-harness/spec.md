## ADDED Requirements

### Requirement: Multi-scenario selection in one invocation

The harness SHALL run more than one scenario from a single invocation. It MUST accept `--scenario` more than once and MUST accept a comma-separated list as a single value; it MUST also accept an `--all` flag that selects every catalog scenario. `--all` and an explicit `--scenario` set MUST be mutually exclusive. For each selected scenario the harness MUST report PASS, FAIL, or SKIP individually, print an aggregate summary, and exit non-zero if any selected scenario FAILED. A SKIP (topology unsupported in the chosen mode) MUST NOT, on its own, cause a non-zero exit. A single `--scenario <name>` invocation MUST behave exactly as before.

#### Scenario: --all runs every catalog scenario for the mode

- **WHEN** the harness is invoked with `--mode <mode> --all`
- **THEN** it runs every catalog scenario, reporting each as PASS/FAIL/SKIP, where scenarios whose topology the mode cannot express are SKIPPED rather than run

#### Scenario: Multiple --scenario values run each selected scenario

- **WHEN** the harness is invoked with `--mode <mode> --scenario a --scenario b` (or `--scenario a,b`)
- **THEN** it runs scenarios `a` and `b` and no others, reporting a result for each

#### Scenario: Aggregate exit reflects any failure

- **WHEN** a multi-scenario run completes with at least one scenario reporting FAIL
- **THEN** the harness prints a summary listing each scenario's result and exits non-zero

#### Scenario: --all and explicit --scenario together is rejected

- **WHEN** the harness is invoked with both `--all` and one or more `--scenario` values
- **THEN** it exits non-zero with a usage error and runs nothing
