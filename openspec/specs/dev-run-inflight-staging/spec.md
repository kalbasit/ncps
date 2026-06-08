# dev-run-inflight-staging Specification

## Purpose

Expose the in-flight NAR staging feature through the `dev-scripts/run.py` development harness so that local and integration test drivers can enable, observe, and exercise staging behavior across spawned `serve` instances.

## Requirements

### Requirement: dev-run exposes an in-flight staging toggle

`dev-scripts/run.py` SHALL accept a boolean `--inflight-staging` flag (default off). When the flag is set, the harness SHALL append `--cache-inflight-staging-enabled` to the `serve` command line of every spawned instance. When the flag is unset, the harness SHALL NOT pass any `--cache-inflight-staging-*` argument, leaving the feature off and the Go-side `retention` (5m) and `part-size` (8 MiB) defaults untouched.

#### Scenario: Flag enabled propagates to every instance
- **WHEN** `run.py --inflight-staging` is invoked (with any `--replicas` count)
- **THEN** each spawned `serve` process receives `--cache-inflight-staging-enabled` in its argument list

#### Scenario: Flag omitted leaves staging off
- **WHEN** `run.py` is invoked without `--inflight-staging`
- **THEN** no `--cache-inflight-staging-enabled`, `--cache-inflight-staging-retention`, or `--cache-inflight-staging-part-size` argument appears on any spawned `serve` command line

#### Scenario: Retention and part-size are not exposed
- **WHEN** the user inspects `run.py --help`
- **THEN** only `--inflight-staging` is offered; no retention or part-size tuning flags are present

### Requirement: dev-run records staging state for test drivers

The harness SHALL surface the effective `--inflight-staging` state both in the startup banner and in the persisted `state.json`, alongside the existing `cdc`/`locker` fields, so external test drivers can read whether staging was enabled for a run.

#### Scenario: State file reflects enabled staging
- **WHEN** `run.py --inflight-staging --locker redis --replicas 2` starts successfully
- **THEN** `var/ncps/state.json` contains a truthy `inflight_staging` field

#### Scenario: State file reflects disabled staging
- **WHEN** `run.py` starts without `--inflight-staging`
- **THEN** `var/ncps/state.json` contains a falsy `inflight_staging` field

#### Scenario: Startup banner shows staging state
- **WHEN** the harness prints its startup summary
- **THEN** the banner includes a line indicating whether in-flight staging is enabled

### Requirement: dev-run does not add staging guard rails

The harness SHALL pass `--inflight-staging` through without enforcing locker constraints, mirroring the Go-side guard where the feature self-disables on single-instance (non-distributed locker) deployments. Combining `--inflight-staging` with `--locker local` SHALL NOT be a harness error.

#### Scenario: Staging with local locker is inert but not rejected
- **WHEN** `run.py --inflight-staging --locker local` is invoked
- **THEN** the harness starts normally and passes `--cache-inflight-staging-enabled`, relying on the Go activation guard to keep the feature dormant
