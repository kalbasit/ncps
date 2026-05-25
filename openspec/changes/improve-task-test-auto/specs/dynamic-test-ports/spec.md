## ADDED Requirements

### Requirement: `nix run .#test-deps` starts backing services on caller-supplied ports
The `test-deps` process-compose profile SHALL read all service ports from environment
variables at launch time rather than using compiled-in defaults.  The profile MUST accept:
- `NCPS_TEST_S3_PORT` — Garage/S3 API port
- `GARAGE_RPC_PORT` — Garage internal RPC port
- `GARAGE_ADMIN_PORT` — Garage admin API port
- `PGPORT` — PostgreSQL port
- `MYSQL_TCP_PORT` — MariaDB port
- `REDIS_PORT` — Redis port
- `TEST_PC_PORT` — process-compose HTTP control port (used for `process-compose down`)

#### Scenario: Services start on caller-provided ports
- **WHEN** `NCPS_TEST_S3_PORT=19000 PGPORT=15432 MYSQL_TCP_PORT=13306 REDIS_PORT=16379 nix run .#test-deps -- up --detached --tui=false -p 17000` is executed
- **THEN** Garage listens on 19000, PostgreSQL on 15432, MariaDB on 13306, Redis on 16379, and process-compose accepts control commands on port 17000

#### Scenario: Missing port env vars fall back to defaults
- **WHEN** `nix run .#test-deps -- up --detached --tui=false -p 17000` is executed with no port env vars set
- **THEN** services bind to their default ports (9000, 5432, 3306, 6379) and the profile behaves identically to the interactive `nix run .#deps`

### Requirement: `process-compose down` stops all services for a given control port
Stopping via `process-compose down -p $TEST_PC_PORT` SHALL terminate all child service
processes (Garage, PostgreSQL, MariaDB, Redis) managed by that process-compose instance.

#### Scenario: Clean teardown via control port
- **WHEN** `process-compose down -p $TEST_PC_PORT` is executed after `nix run .#test-deps -- up --detached --tui=false -p $TEST_PC_PORT`
- **THEN** all four backing services are stopped and no orphan processes remain

### Requirement: Ready markers for `test-deps` init processes are instance-unique
Init processes in the `test-deps` profile (postgres-init, mariadb-init) SHALL write ready
marker files that include `${TEST_PC_PORT}` in the filename to avoid collisions between
sequential runs.

#### Scenario: Sequential runs do not see stale markers
- **WHEN** `task test:auto` is run twice in sequence (first run completes before second starts)
- **THEN** the second run's init processes create fresh marker files and do not detect the first run's markers as valid

### Requirement: `enable-s3-tests` emits endpoint URL using current `NCPS_TEST_S3_PORT`
When `eval "$(enable-s3-tests)"` is called in a shell where `NCPS_TEST_S3_PORT` is already
exported, the script SHALL emit `export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:$NCPS_TEST_S3_PORT"`.
When `NCPS_TEST_S3_PORT` is not set, it SHALL fall back to port 9000.

#### Scenario: Dynamic port already exported
- **WHEN** `NCPS_TEST_S3_PORT=19000` is set in the shell and `eval "$(enable-s3-tests)"` is run
- **THEN** `NCPS_TEST_S3_ENDPOINT` is set to `http://127.0.0.1:19000`

#### Scenario: No port env var — default port used
- **WHEN** `NCPS_TEST_S3_PORT` is not set and `eval "$(enable-s3-tests)"` is run
- **THEN** `NCPS_TEST_S3_ENDPOINT` is set to `http://127.0.0.1:9000`

### Requirement: `enable-postgres-tests` emits connection URL using current `PGPORT`
When `eval "$(enable-postgres-tests)"` is called in a shell where `PGPORT` is already
exported, the script SHALL emit a connection URL referencing that port.
When `PGPORT` is not set, it SHALL fall back to port 5432.

#### Scenario: Dynamic port already exported
- **WHEN** `PGPORT=15432` is set in the shell and `eval "$(enable-postgres-tests)"` is run
- **THEN** the emitted connection URL references port 15432

#### Scenario: No port env var — default port used
- **WHEN** `PGPORT` is not set and `eval "$(enable-postgres-tests)"` is run
- **THEN** the emitted connection URL references port 5432

### Requirement: `enable-mysql-tests` emits connection URL using current `MYSQL_TCP_PORT`
When `eval "$(enable-mysql-tests)"` is called in a shell where `MYSQL_TCP_PORT` is already
exported, the script SHALL emit a connection URL referencing that port.
When `MYSQL_TCP_PORT` is not set, it SHALL fall back to port 3306.

#### Scenario: Dynamic port already exported
- **WHEN** `MYSQL_TCP_PORT=13306` is set and `eval "$(enable-mysql-tests)"` is run
- **THEN** the emitted connection URL references port 13306

#### Scenario: No port env var — default port used
- **WHEN** `MYSQL_TCP_PORT` is not set and `eval "$(enable-mysql-tests)"` is run
- **THEN** the emitted connection URL references port 3306
