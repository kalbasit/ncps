## Why

`task test:auto` currently hard-codes the four backing-service ports (Garage/S3 on 9000,
PostgreSQL on 5432, MariaDB on 3306, Redis on 6379).  When any of those ports is already
in use — another developer's `task deps`, a stale process, a parallel CI job on the same
host — the script either silently reuses the existing service (risking cross-contamination
between test runs) or fails with a connection-refused error after a 60-second wait.

The fix is to make `task test:auto` allocate random free ports at runtime, start services
on those ports, run the full integration suite, and tear down cleanly.  This eliminates
port conflicts between parallel invocations and between interactive `task deps` and
automated test runs.

## What Changes

1. **New `nix run .#test-deps` process-compose profile** — a second process-compose
   profile in `nix/process-compose/flake-module.nix` that reads all service ports from
   environment variables (`NCPS_TEST_S3_PORT`, `PGPORT`, `MYSQL_TCP_PORT`, `REDIS_PORT`,
   and the internal Garage ports `GARAGE_RPC_PORT` / `GARAGE_ADMIN_PORT`) instead of
   hard-coding them.  Uses `disable_env_expansion = false` so process-compose expands the
   variables at launch time.  The existing `nix run .#deps` profile is unchanged — it
   remains the fixed-port interactive development tool.

2. **Rewritten `dev-scripts/test-auto.sh`** — allocates 7 free ports simultaneously using
   Python's socket bind-all-then-close pattern, exports them as env vars, starts
   `nix run .#test-deps` in detached mode (`-- up --detached --tui=false -p $TEST_PC_PORT`),
   polls the four test ports until ready, runs `eval "$(enable-integration-tests)"` and
   `go test -race ./...`, then tears down via `process-compose down -p $TEST_PC_PORT`.
   The state-file approach (`/tmp/ncps-test-deps.env`) makes start/stop composable.

3. **Updated `enable-*` devshell scripts** — `enable-s3-tests` and `enable-postgres-tests`
   currently hard-code endpoint URLs with port 9000 / 5432.  They are updated to use
   `${NCPS_TEST_S3_PORT:-9000}` and `${PGPORT:-5432}` so they emit the correct endpoint
   when called in a shell that already has the random ports exported.  Interactive use
   (no port env vars set) falls back to the default ports unchanged.

4. **Updated `Taskfile.yml`** — `test:auto` remains a single entry point but is joined
   by `test:deps:start` and `test:deps:stop` helper tasks for use in other scripts or
   workflows that need to manage the lifecycle separately.

## Capabilities

### New Capabilities
- `dynamic-test-ports`: Ability to start backing services on runtime-selected free ports
  and tear them down cleanly after the test suite completes.

### Modified Capabilities
- `task-workflow`: `test:auto`, `test:deps:start`, and `test:deps:stop` task
  implementations change to use the dynamic-port workflow.

## Impact

- `nix/process-compose/flake-module.nix` — new `process-compose.test-deps` attrset
- `nix/devshells/flake-module.nix` — updated `enable-s3-tests` / `enable-postgres-tests`
- `dev-scripts/test-auto.sh` — rewritten
- `Taskfile.yml` — two new helper tasks (`test:deps:start`, `test:deps:stop`)
- No changes to Go source code, existing tests, or the `nix run .#deps` dev profile
- `nix flake check` and CI are unaffected
