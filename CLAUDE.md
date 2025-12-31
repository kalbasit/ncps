# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ncps (Nix Cache Proxy Server) is a Go application that acts as a local binary cache proxy for Nix. It fetches store paths from upstream caches (like cache.nixos.org) and caches them locally, reducing download times and bandwidth usage.

## Development Commands

### Prerequisites

Uses Nix flakes with direnv (`.envrc` with `use_flake`). Tools available in dev shell: go, golangci-lint, sqlc, sqlfluff, dbmate, delve, watchexec.

### Common Commands

```bash
# Run development server (hot-reload with watchexec)
./dev-scripts/run.sh              # Uses local filesystem storage (default)
./dev-scripts/run.sh local        # Explicitly use local storage
./dev-scripts/run.sh s3           # Use S3/MinIO storage (requires deps to be running)

# Start development dependencies (MinIO for S3 testing, PostgreSQL for database testing)
nix run .#deps                    # Starts MinIO and PostgreSQL with self-validation

# Run tests with race detector
go test -race ./...

# Run a single test
go test -race -run TestName ./pkg/server/...

# Lint code
golangci-lint run
golangci-lint run --fix  # Automatically fix fixable linter issues

# Format code
nix fmt                  # Format all project files (Go, Nix, SQL, etc.)

# Lint SQL files
sqlfluff lint db/query.*.sql              # Lint all SQL query files
sqlfluff lint db/migrations/              # Lint all migration files

# Format SQL files
sqlfluff format db/query.*.sql            # Format all SQL query files
sqlfluff format db/migrations/            # Format all migration files

# Generate SQL code (after modifying db/query.sql or migrations)
sqlc generate

# Create new database migrations (creates timestamped migration files)
dbmate --migrations-dir db/migrations/sqlite new migration_name
dbmate --migrations-dir db/migrations/postgres new migration_name
dbmate --migrations-dir db/migrations/mysql new migration_name

# Run database migrations manually
dbmate --url "sqlite:/path/to/your/db.sqlite" --migrations-dir db/migrations/sqlite up
dbmate --url "postgresql://user:pass@localhost:5432/ncps" --migrations-dir db/migrations/postgres up
dbmate --url "mysql://user:pass@localhost:3306/ncps" --migrations-dir db/migrations/mysql up

# Build
go build .

# Build with Nix
nix build
```

## Development Workflow

### Storage Backends

The development server (`./dev-scripts/run.sh`) supports two storage backends:

**Local Storage (default):**

- No external dependencies required
- Uses temporary directory for cache storage
- Ideal for quick testing and development
- Storage is ephemeral (cleaned up on script exit)

**S3 Storage (MinIO):**

- Requires running MinIO via `nix run .#deps`
- Tests S3-compatible storage implementation
- Uses MinIO server on `127.0.0.1:9000`
- Pre-configured with test credentials and bucket
- Includes self-validation to ensure proper setup

### Dependency Management (process-compose)

The project uses [process-compose-flake](https://github.com/Platonic-Systems/process-compose-flake) for managing development dependencies. Currently provides:

**`nix run .#deps`** - Starts development services:

**MinIO (S3-compatible storage):**

- Ephemeral storage in temporary directory
- MinIO server on port 9000, console on port 9001
- Pre-configured test bucket (`test-bucket`)
- Test credentials: `test-access-key` / `test-secret-key`
- Self-validation checks:
  - Access key authentication
  - Public access blocking (security verification)
  - Signed URL generation and access

**PostgreSQL (database):**

- Ephemeral storage in temporary directory
- PostgreSQL server on port 5432
- Pre-configured test database (`test-db`)
- Test credentials: `test-user` / `test-password`
- Connection URL: `postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable`
- Self-validation checks:
  - Connection test
  - Table operations (CREATE, INSERT)
  - Query verification

**MySQL/MariaDB (database):**

- Ephemeral storage in temporary directory
- MariaDB server on port 3306
- Pre-configured test database (`test-db`)
- Test credentials: `test-user` / `test-password`
- Connection URL: `mysql://test-user:test-password@127.0.0.1:3306/test-db`
- Self-validation checks:
  - Connection test
  - Table operations (CREATE, INSERT)
  - Query verification

Configuration in `nix/process-compose/flake-module.nix` defines:

- `minio-server` process - MinIO server with health checks
- `create-buckets` process - Bucket creation and validation
- `postgres-server` process - PostgreSQL server with health checks
- `init-database` process - PostgreSQL database and user creation with validation
- `mariadb-server` process - MariaDB server with health checks
- `init-mariadb` process - MariaDB database and user creation with validation

The service configurations match the test environment variables to ensure consistency between dependency setup and application configuration.

### CI/CD and GitHub Actions

The project uses GitHub Actions for continuous integration. The workflows are configured to optimize for **Graphite-style stacked PRs**.

**Key Workflows:**

- `.github/workflows/ci.yml` - Main CI workflow (runs `nix flake check` and Docker builds)
- `.github/workflows/semantic-pull-request.yml` - PR title validation
- `.github/workflows/build.yml` - Docker image builds on main branch
- `.github/workflows/releases.yml` - Release automation

**Important:** CI workflows are configured to **only run on PRs targeting `main`**:

```yaml
on:
  pull_request:
    branches:
      - main
```

This prevents wasted CI resources when using Graphite stacks where PRs merge into each other:

```
PR #7: feature-g → main          ← ✅ CI runs (only this one)
PR #6: feature-f → feature-g     ← ❌ CI skipped
PR #5: feature-e → feature-f     ← ❌ CI skipped
...
```

**When modifying workflows:** Maintain the `branches: [main]` restriction to keep CI efficient for stacked PR workflows.

## Architecture

### Package Structure

- `cmd/` - CLI commands (serve, global flags, OpenTelemetry bootstrap)
- `pkg/cache/` - Core caching logic and upstream cache fetching
- `pkg/storage/` - Storage abstraction layer with implementations:
  - `storage/local/` - Local filesystem storage
  - `storage/s3/` - S3-compatible storage (including MinIO)
- `pkg/server/` - HTTP server using Chi router
- `pkg/database/` - Database abstraction layer supporting multiple engines (sqlc-generated code)
  - `database/sqlitedb/` - SQLite-specific implementation
  - `database/postgresdb/` - PostgreSQL-specific implementation
  - `database/mysqldb/` - MySQL/MariaDB-specific implementation
- `pkg/nar/` - NAR (Nix ARchive) format handling
- `db/migrations/` - Database migration files
  - `migrations/sqlite/` - SQLite migration files
  - `migrations/postgres/` - PostgreSQL migration files
  - `migrations/mysql/` - MySQL/MariaDB migration files
- `db/query.sqlite.sql` - SQLite queries for sqlc code generation
- `db/query.postgres.sql` - PostgreSQL queries for sqlc code generation
- `db/query.mysql.sql` - MySQL/MariaDB queries for sqlc code generation

### Key Interfaces (pkg/storage/store.go)

Storage uses interface-based abstraction:

- `ConfigStore` - Secret key storage
- `NarInfoStore` - NarInfo metadata storage
- `NarStore` - NAR file storage

Both local and S3 backends implement these interfaces.

### Database

Supports multiple database engines via sqlc for type-safe SQL:

- **SQLite** (default): Embedded database, no external dependencies
- **PostgreSQL**: Scalable relational database for production deployments
- **MySQL/MariaDB**: Popular open-source relational database for production deployments

Database selection is done via URL scheme in the `--cache-database-url` flag:

- SQLite: `sqlite:/path/to/db.sqlite`
- PostgreSQL: `postgresql://user:password@host:port/database`
- MySQL/MariaDB: `mysql://user:password@host:port/database`

Schema in `db/schema.sql`, engine-specific queries in `db/query.sqlite.sql`, `db/query.postgres.sql`, and `db/query.mysql.sql`. Run `sqlc generate` after modifying queries.

**Creating Database Migrations:**

When creating new database migrations, always use `dbmate new` to generate properly timestamped migration files:

```bash
dbmate --url sqlite:/path/to/db.sqlite new migration_name
dbmate --url postgresql://user:pass@localhost:5432/ncps new migration_name
dbmate --url mysql://user:pass@localhost:3306/ncps new migration_name
```

This creates timestamped migration files (e.g., `20251230223951_migration_name.sql`) with the standard dbmate template:

```sql
-- migrate:up


-- migrate:down

```

**How the dbmate wrapper works:**

The `dbmate` command in the development environment and Docker images is actually a wrapper (separate `dbmate-wrapper` binary in `nix/dbmate-wrapper/`). The wrapper automatically detects the migrations directory based on the database URL scheme:

- `sqlite:` → uses `db/migrations/sqlite`
- `postgresql:` or `postgres:` → uses `db/migrations/postgres`
- `mysql:` → uses `db/migrations/mysql`

This means you **don't need to specify `--migrations-dir` manually** - the wrapper handles it automatically!

If you need to override the auto-detection, you can still provide `--migrations-dir` explicitly.

**Implementation details:**

The wrapper is a standalone Go program in `nix/dbmate-wrapper/` that:

- Parses the `--url` flag to determine the database type (sqlite, postgres, mysql)
- Uses the `NCPS_DB_MIGRATIONS_DIR` environment variable to locate the base migrations directory
  - In Docker: set to `/share/ncps/db/migrations` (static path in container)
  - In dev shell: set dynamically via `shellHook` to `$(git rev-parse --show-toplevel)/db/migrations` (repo root)
  - This ensures migration changes are immediately visible without rebuilding
- Automatically sets the `DBMATE_MIGRATIONS_DIR` environment variable to the appropriate database-specific path:
  - Example: `${NCPS_DB_MIGRATIONS_DIR}/sqlite` or `${NCPS_DB_MIGRATIONS_DIR}/postgres`
- Calls the real `dbmate` binary (consistently renamed to `dbmate.real` in both dev and Docker)
- Respects user overrides: if `DBMATE_MIGRATIONS_DIR` is already set or `--migrations-dir` is provided, the wrapper passes through without modification
- This keeps the wrapper simple and doesn't require rebuilding ncps to update it

**IMPORTANT:** Never manually create migration files by copying existing ones, as this will result in incorrect timestamps. Always use `dbmate new` to ensure proper chronological ordering.

## Code Quality

### Linting

Strict linting via golangci-lint with 30+ linters enabled (see `.golangci.yml`). Key linters: err113, exhaustive, gosec, paralleltest, testpackage.

**IMPORTANT**: Always use `golangci-lint run --fix` first to automatically fix fixable issues before doing manual fixes. This saves tokens and is more efficient.

### Formatting

Uses gofumpt, goimports, and gci for import ordering (standard → default → alias → localmodule). SQL files are formatted using sqlfluff.

**IMPORTANT**: Always use `nix fmt` to automatically format project files (Go, Nix, etc.) before making manual edits. For SQL files specifically, use `sqlfluff format` to fix formatting issues.

### Testing

- Tests use testify for assertions
- Race detector enabled (`go test -race`)
- Test files use `_test` package suffix (testpackage linter)
- Parallel tests encouraged (paralleltest linter)

#### S3 Integration Tests

S3 integration tests require MinIO to be running. The tests are automatically skipped if the required environment variables are not set.

**For local development:**

```bash
# Start MinIO (in a separate terminal)
nix run .#deps

# Run tests with S3 integration enabled
export NCPS_TEST_S3_BUCKET="test-bucket"
export NCPS_TEST_S3_ENDPOINT="http://127.0.0.1:9000"
export NCPS_TEST_S3_REGION="us-east-1"
export NCPS_TEST_S3_ACCESS_KEY_ID="test-access-key"
export NCPS_TEST_S3_SECRET_ACCESS_KEY="test-secret-key"
go test -race ./pkg/storage/s3
```

**For Nix builds and CI:**

MinIO is automatically started during the test phase when building with Nix:

```bash
# Runs all checks including S3 integration tests
nix flake check

# Build package (includes test phase with MinIO)
nix build
```

The Nix build (`nix/packages/ncps.nix`) automatically:

1. Starts MinIO server in the `preCheck` phase
1. Creates test bucket and credentials
1. Exports S3 test environment variables
1. Runs all tests (including S3 integration tests)
1. Stops MinIO in the `postCheck` phase

This setup ensures:

- S3 integration tests run in CI/CD (GitHub Actions workflows)
- `nix flake check` includes S3 testing
- Runtime usage (`nix run github:kalbasit/ncps`) is unaffected
- Docker builds (`.#docker`) are unaffected
- Tests are isolated and don't interfere with each other (unique hash-based keys)

#### PostgreSQL Integration Tests

PostgreSQL integration tests require PostgreSQL to be running. The tests are automatically skipped if the required environment variable is not set.

**For local development:**

```bash
# Start PostgreSQL (in a separate terminal)
nix run .#deps

# Run tests with PostgreSQL integration enabled
export NCPS_TEST_POSTGRES_URL="postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable"
go test -race ./pkg/database
```

**For Nix builds and CI:**

PostgreSQL is automatically started during the test phase when building with Nix:

```bash
# Runs all checks including PostgreSQL integration tests
nix flake check

# Build package (includes test phase with PostgreSQL)
nix build
```

The Nix build (`nix/packages/ncps.nix`) automatically:

1. Starts PostgreSQL server in the `preCheck` phase
1. Creates test database and user
1. Exports PostgreSQL test environment variable
1. Runs all tests (including PostgreSQL integration tests)
1. Stops PostgreSQL in the `postCheck` phase

This setup ensures:

- PostgreSQL integration tests run in CI/CD (GitHub Actions workflows)
- `nix flake check` includes PostgreSQL testing
- Both SQLite and PostgreSQL database implementations are tested
- Tests are isolated with unique random hashes
- Migrations are validated against both database engines

#### MySQL/MariaDB Integration Tests

MySQL/MariaDB integration tests require MariaDB to be running. The tests are automatically skipped if the required environment variable is not set.

**For local development:**

```bash
# Start MariaDB (in a separate terminal)
nix run .#deps

# Run tests with MySQL integration enabled
export NCPS_TEST_MYSQL_URL="mysql://test-user:test-password@127.0.0.1:3306/test-db"
go test -race ./pkg/database
```

**For Nix builds and CI:**

MariaDB is automatically started during the test phase when building with Nix:

```bash
# Runs all checks including MySQL integration tests
nix flake check

# Build package (includes test phase with MariaDB)
nix build
```

The Nix build (`nix/packages/ncps.nix`) automatically:

1. Starts MariaDB server in the `preCheck` phase
1. Creates test database and user
1. Exports MySQL test environment variable
1. Runs all tests (including MySQL integration tests)
1. Stops MariaDB in the `postCheck` phase

This setup ensures:

- MySQL/MariaDB integration tests run in CI/CD (GitHub Actions workflows)
- `nix flake check` includes MySQL testing
- All three database implementations (SQLite, PostgreSQL, MySQL) are tested
- Tests are isolated with unique random hashes
- Migrations are validated against all database engines

## Configuration

Supports YAML/TOML/JSON config files. See `config.example.yaml` for all options. Key configuration areas:

- Cache settings (hostname, data-path, database-url, max-size)
- Upstream caches and public keys
- OpenTelemetry and Prometheus metrics
- Server address and security options (PUT/DELETE verb control)
