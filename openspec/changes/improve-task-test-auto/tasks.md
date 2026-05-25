## 1. Add `process-compose.test-deps` Nix profile

- [x] 1.1 Add `testDepsEnvironment` attrsets in `nix/process-compose/flake-module.nix` for each service, using `"\${PORT_VAR}"` (backslash-escaped) placeholders for all port values
- [x] 1.2 Add `process-compose.test-deps` block with `settings.disable_env_expansion = false` and all service definitions referencing those environments
- [x] 1.3 Convert Garage healthcheck from `http_get { port = 3903; }` to `exec.command = "curl -sf http://127.0.0.1:\${GARAGE_ADMIN_PORT}/health"` (since `http_get.port` cannot be an env var)
- [x] 1.4 Update postgres-init inline command to write marker file at `''${TMPDIR:-/tmp}/ncps-postgres-''${TEST_PC_PORT:-0}.ready` and check for it in the readiness probe
- [x] 1.5 Update mariadb-init inline command to write marker file at `''${TMPDIR:-/tmp}/ncps-mysql-''${TEST_PC_PORT:-0}.ready` and check for it in the readiness probe
- [x] 1.6 Update postgres and mariadb server healthcheck commands to use `''${PGPORT:-5432}` and `''${MYSQL_TCP_PORT:-3306}` respectively
- [x] 1.7 Update Redis healthcheck command to use `''${REDIS_PORT:-6379}`

## 2. Update `enable-*` devshell helper scripts

- [x] 2.1 Update `enable-s3-tests` in `nix/devshells/flake-module.nix` to emit `NCPS_TEST_S3_ENDPOINT` using `${NCPS_TEST_S3_PORT:-9000}` and also re-export `NCPS_TEST_S3_PORT` with the same value
- [x] 2.2 Update `enable-postgres-tests` to emit the connection URL using `${PGPORT:-5432}`
- [x] 2.3 Update `enable-mysql-tests` to emit the connection URL using `${MYSQL_TCP_PORT:-3306}`

## 3. Rewrite `dev-scripts/test-auto.sh`

- [x] 3.1 Replace port-check logic with Python free-port allocation (7 ports: S3, Garage-RPC, Garage-Admin, PG, MySQL, Redis, PC-control)
- [x] 3.2 Export all port env vars (`NCPS_TEST_S3_PORT`, `GARAGE_RPC_PORT`, `GARAGE_ADMIN_PORT`, `PGPORT`, `MYSQL_TCP_PORT`, `REDIS_PORT`, `TEST_PC_PORT`) and also `NCPS_TEST_S3_ENDPOINT`
- [x] 3.3 Write port assignments to `${TMPDIR:-/tmp}/ncps-test-deps.env` state file
- [x] 3.4 Start `nix run .#test-deps -- up --detached --tui=false -p $TEST_PC_PORT`
- [x] 3.5 Poll all four test ports (S3, PG, MySQL, Redis) until ready, max 120s, with `kill -0` fail-fast check on the process-compose PID
- [x] 3.6 Replace `cleanup` trap with `process-compose down -p $TEST_PC_PORT` (falling back to `nix run .#test-deps -- down -p $TEST_PC_PORT`) plus removal of state file
- [x] 3.7 Keep `eval "$(enable-integration-tests)"` and `go test -race ./...` unchanged

## 4. Update `Taskfile.yml`

- [x] 4.1 Add `test:deps:start` task that calls `bash dev-scripts/test-auto.sh --start-only` (or delegates entirely to a start-only shell script)
- [x] 4.2 Add `test:deps:stop` task that sources the state file and runs `process-compose down -p $TEST_PC_PORT`
- [x] 4.3 Update `test:auto` desc to reflect that it always starts fresh services on random ports

## 5. Update `CLAUDE.md`

- [x] 5.1 Update the `task test:auto` description in the commands table to note that it always starts fresh isolated services

## 6. Verify

- [x] 6.1 Run `task fmt` and confirm it exits 0
- [x] 6.2 Run `task lint` and confirm it exits 0
- [x] 6.3 Run `task test` (unit tests) and confirm it exits 0
- [ ] 6.4 Manually verify `task test:auto` allocates random ports, runs tests, and tears down (smoke test)
