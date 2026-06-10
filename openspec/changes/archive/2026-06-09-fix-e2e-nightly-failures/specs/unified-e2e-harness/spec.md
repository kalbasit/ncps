## MODIFIED Requirements

### Requirement: Dependency lifecycle and result reporting

The harness SHALL own the lifecycle of the backing dependencies for the selected mode and report results uniformly. In `local` mode it MUST start the fixed-port backends (`nix run .#deps`: S3/Garage, PostgreSQL, MariaDB, Redis) when they are not already running and stop the ones it started on exit. In `kubernetes` mode it MUST provision the cluster dependencies. It MUST report per-scenario and per-phase PASS/FAIL and MUST exit non-zero if any scenario or phase fails.

When a single `local`-mode invocation runs more than one scenario (a multi-scenario or "all" run), the harness MUST start the backends it manages **once** before running the scenarios and stop them **once** after the last scenario, rather than starting and stopping them per scenario. The single shared startup MUST include Redis whenever any selected scenario requires it. This avoids paying a full cold backend boot (ephemeral `mktemp` data dirs: `initdb`, `mariadb-install-db`, garage layout) for every scenario.

The readiness wait MUST tolerate a cold backend boot on a resource-constrained CI runner: the timeout MUST be at least 300 seconds. When the backends do not become ready within the timeout, the harness MUST emit diagnostics identifying which backend(s) were not ready — at minimum the process-compose process list and the per-process logs — before failing, instead of reporting only an opaque "services not ready" message.

The kubernetes validation HTTP probes MUST tolerate a transient post-deploy server error: the narinfo and NAR fetches MUST retry a bounded number of times with short backoff on connection errors and 5xx responses, so that a transient error during ncps warm-up or seeding does not fail an otherwise-healthy scenario.

#### Scenario: Dependencies are started and torn down

- **WHEN** the harness runs a scenario that needs backends it had to start
- **THEN** the required services are confirmed reachable before the scenario runs, and the services the harness started are stopped on exit (success or failure)

#### Scenario: Multi-scenario local run starts backends once

- **WHEN** a single `local`-mode invocation runs more than one scenario (e.g. `--all`)
- **THEN** the harness starts the backends it manages once before the first scenario and stops them once after the last scenario, not once per scenario, and includes Redis if any selected scenario needs it

#### Scenario: Readiness wait tolerates a cold CI boot

- **WHEN** the managed backends are starting cold on a resource-constrained runner
- **THEN** the harness waits at least 300 seconds for all required ports to become reachable before declaring a readiness failure

#### Scenario: Readiness failure surfaces backend diagnostics

- **WHEN** the managed backends do not become ready within the readiness timeout
- **THEN** the harness emits the process-compose process list and per-process logs identifying the unready backend(s) before failing, not just an opaque "services not ready" message

#### Scenario: Kubernetes validation retries a transient post-deploy error

- **WHEN** a narinfo or NAR fetch during kubernetes validation returns a connection error or a 5xx response shortly after deploy
- **THEN** the harness retries the fetch a bounded number of times with short backoff and only fails the check if the error persists

#### Scenario: Failure produces a non-zero exit

- **WHEN** any scenario phase asserts a failure (incomplete NAR, wrong DB invariant, missing activation, etc.)
- **THEN** the harness reports that phase as FAILED and the overall process exits non-zero

#### Scenario: Resources are cleaned up on failure

- **WHEN** a scenario aborts mid-run
- **THEN** the harness still tears down the ncps instances and the dependencies it started, leaving no orphaned processes (local) or installs (kubernetes)
