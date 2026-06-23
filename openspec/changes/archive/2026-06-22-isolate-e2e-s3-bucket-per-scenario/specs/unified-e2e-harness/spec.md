## ADDED Requirements

### Requirement: Per-scenario storage isolation in kubernetes mode

The harness MUST isolate the S3 storage backend per scenario in `kubernetes` mode, so that no scenario observes objects written by another scenario. Each scenario MUST deploy against its own S3 bucket whose name is derived deterministically from the scenario name, mirroring the existing per-scenario database isolation. The harness MUST create each per-scenario bucket and grant the access key idempotently during dependency/garage setup, and each scenario's generated Helm values MUST reference its own bucket. A scenario that downloads and chunks a NAR MUST therefore start from empty storage, so a CDC scenario re-downloads and chunks its NARs rather than finding whole-file residue left by an earlier non-CDC scenario. The S3 storage validation (counting `store/chunk/`) MUST consequently reflect only the running scenario's writes and MUST NOT pass on residue from a previous scenario.

#### Scenario: Each kubernetes scenario uses its own bucket

- **WHEN** two kubernetes scenarios that use S3 storage run in the same cluster
- **THEN** each scenario deploys against a distinct bucket derived from its name, and neither scenario can read objects written by the other

#### Scenario: CDC scenario starts from empty storage

- **WHEN** a CDC scenario runs after a non-CDC scenario that downloaded the same test NARs as whole files
- **THEN** the CDC scenario's bucket contains no residual whole-file NARs, so ncps re-downloads and eager-chunks them and the per-scenario database ends with a non-zero `chunks` count

#### Scenario: S3 storage check reflects only the running scenario

- **WHEN** the harness validates S3 storage for a scenario by counting `store/chunk/`
- **THEN** the count reflects only objects that scenario wrote and does not pass on chunks left by a previously-run scenario
