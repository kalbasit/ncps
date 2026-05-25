## MODIFIED Requirements

### Requirement: `task test:auto` auto-starts and tears down backend services
Running `task test:auto` MUST allocate random free ports, start fresh backing services on
those ports via `nix run .#test-deps`, run the full integration suite, and tear down the
services on exit regardless of test outcome.  `task test:auto` SHALL NOT reuse services
already running on fixed ports — it always provisions its own isolated instances.

#### Scenario: Always starts fresh services on random ports
- **WHEN** a developer runs `task test:auto`
- **THEN** 7 free ports are allocated, `nix run .#test-deps` is started in detached mode on those ports, the script waits for all four test ports to be ready, the integration suite runs, and `process-compose down` is called on exit

#### Scenario: Exit code propagated
- **WHEN** the test suite exits with a non-zero code (test failure)
- **THEN** `task test:auto` exits with the same non-zero code

#### Scenario: Teardown on failure
- **WHEN** `task test:auto` is interrupted (Ctrl-C) or the test suite fails
- **THEN** `process-compose down -p $TEST_PC_PORT` is called and backing services are stopped

## ADDED Requirements

### Requirement: `task test:deps:start` starts backing services on random free ports
Running `task test:deps:start` MUST allocate random free ports, start `nix run .#test-deps`
in detached mode, wait until all services are healthy, and write the port assignments to a
state file at `${TMPDIR:-/tmp}/ncps-test-deps.env`.

#### Scenario: Successful start writes state file
- **WHEN** `task test:deps:start` completes successfully
- **THEN** `${TMPDIR:-/tmp}/ncps-test-deps.env` exists and contains the port assignments for all services

#### Scenario: Services ready within 120 seconds
- **WHEN** `task test:deps:start` is run on a machine where the services can start
- **THEN** all four service ports are reachable within 120 seconds and the task exits 0

### Requirement: `task test:deps:stop` stops the services started by `task test:deps:start`
Running `task test:deps:stop` MUST read the state file written by `task test:deps:start` and
call `process-compose down -p $TEST_PC_PORT` to stop the process-compose instance.

#### Scenario: Stops running services
- **WHEN** `task test:deps:stop` is run after `task test:deps:start`
- **THEN** all four backing services are stopped

#### Scenario: No-op when state file is absent
- **WHEN** `task test:deps:stop` is run with no state file present
- **THEN** the task exits 0 with an informational message and takes no action
