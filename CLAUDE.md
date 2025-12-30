# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ncps (Nix Cache Proxy Server) is a Go application that acts as a local binary cache proxy for Nix. It fetches store paths from upstream caches (like cache.nixos.org) and caches them locally, reducing download times and bandwidth usage.

## Development Commands

### Prerequisites

Uses Nix flakes with direnv (`.envrc` with `use_flake`). Tools available in dev shell: go, golangci-lint, sqlc, dbmate, delve, watchexec.

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

# Generate SQL code (after modifying db/query.sql or migrations)
sqlc generate

# Run database migrations manually
dbmate --url "sqlite:/path/to/your/db.sqlite" --migrations-dir db/migrations/sqlite up

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

Configuration in `nix/process-compose/flake-module.nix` defines:

- `minio-server` process - MinIO server with health checks
- `create-buckets` process - Bucket creation and validation
- `postgres-server` process - PostgreSQL server with health checks
- `init-database` process - Database and user creation with validation

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
- `pkg/nar/` - NAR (Nix ARchive) format handling
- `db/migrations/` - Database migration files
  - `migrations/sqlite/` - SQLite migration files
- `db/query.sqlite.sql` - SQLite queries for sqlc code generation

### Key Interfaces (pkg/storage/store.go)

Storage uses interface-based abstraction:

- `ConfigStore` - Secret key storage
- `NarInfoStore` - NarInfo metadata storage
- `NarStore` - NAR file storage

Both local and S3 backends implement these interfaces.

### Database

Supports multiple database engines via sqlc for type-safe SQL:

- **SQLite** (default): Embedded database, no external dependencies

Database selection is done via URL scheme in the `--cache-database-url` flag:

- SQLite: `sqlite:/path/to/db.sqlite`

Schema in `db/schema.sql`, engine-specific queries in `db/query.sqlite.sql`. Run `sqlc generate` after modifying queries.

## Code Quality

### Linting

Strict linting via golangci-lint with 30+ linters enabled (see `.golangci.yml`). Key linters: err113, exhaustive, gosec, paralleltest, testpackage.

**IMPORTANT**: Always use `golangci-lint run --fix` first to automatically fix fixable issues before doing manual fixes. This saves tokens and is more efficient.

### Formatting

Uses gofumpt, goimports, and gci for import ordering (standard → default → alias → localmodule).

**IMPORTANT**: Always use `nix fmt` to automatically format project files (Go, Nix, etc.) before making manual edits.

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

## Configuration

Supports YAML/TOML/JSON config files. See `config.example.yaml` for all options. Key configuration areas:

- Cache settings (hostname, data-path, database-url, max-size)
- Upstream caches and public keys
- OpenTelemetry and Prometheus metrics
- Server address and security options (PUT/DELETE verb control)
