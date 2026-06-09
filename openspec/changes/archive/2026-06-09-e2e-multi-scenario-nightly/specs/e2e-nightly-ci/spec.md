## ADDED Requirements

### Requirement: Scheduled nightly e2e execution

The repository SHALL provide a GitHub Actions workflow that runs the full unified e2e catalog on a nightly schedule. The workflow MUST be triggered by `schedule` (cron) and MUST run the harness with `--all` so every catalog scenario supporting the leg's mode is exercised. It MUST NOT run on pull requests and MUST NOT be part of `nix flake check`, keeping the harness off the per-PR hot path.

#### Scenario: Nightly cron triggers the full catalog

- **WHEN** the nightly schedule fires
- **THEN** the workflow runs `nix run .#e2e -- --mode <mode> --all` for each matrix leg, reporting per-scenario PASS/FAIL/SKIP

#### Scenario: The workflow never runs on pull requests

- **WHEN** a pull request is opened or updated
- **THEN** the nightly e2e workflow does not run and `nix flake check` does not invoke it

### Requirement: Mode matrix coverage

The workflow SHALL run the catalog as a matrix over the harness modes `local` and `kubernetes`. Each leg MUST run independently so a failure in one mode does not prevent the other mode's results from being produced, and the kubernetes leg MUST provision its Kind cluster on the runner.

#### Scenario: Both modes run as independent legs

- **WHEN** the nightly workflow executes
- **THEN** it runs a `local` leg and a `kubernetes` leg as separate matrix jobs, each reporting its own result

#### Scenario: One mode failing does not cancel the other

- **WHEN** the `kubernetes` leg fails
- **THEN** the `local` leg still runs to completion and reports its own PASS/FAIL

### Requirement: Commit-tested deduplication

The workflow SHALL record the `main` commit SHA it last successfully tested and SHALL skip a scheduled run when `main` has not advanced since that recorded SHA, so the same commit is never e2e-tested on two consecutive scheduled runs. The recorded SHA MUST only be written after a fully successful run, so a failed run does not suppress a retry on the next schedule.

#### Scenario: Unchanged main skips the run

- **WHEN** the scheduled workflow resolves the current `main` SHA and finds it equals the last successfully-tested SHA
- **THEN** the matrix legs are skipped and the run records no new test work

#### Scenario: Advanced main runs and records the new SHA

- **WHEN** `main` has advanced since the last recorded SHA and the matrix legs all pass
- **THEN** the workflow runs the catalog and records the newly-tested SHA for the next schedule to compare against

#### Scenario: A failed run does not record the SHA

- **WHEN** a scheduled run executes but at least one matrix leg fails
- **THEN** the last-tested SHA is left unchanged so the next schedule re-attempts the same commit

### Requirement: Manual dispatch override

The workflow SHALL be manually triggerable via `workflow_dispatch`, and a manual trigger MUST bypass the commit-tested deduplication so an operator can force a full run against the current `main` regardless of whether it changed.

#### Scenario: Manual dispatch forces a run

- **WHEN** an operator triggers the workflow via `workflow_dispatch`
- **THEN** the matrix legs run even if `main` is unchanged since the last recorded SHA
