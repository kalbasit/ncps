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

# Generate SQL code and Database Wrappers (after modifying db/query.sql or migrations)
sqlc generate
go generate ./pkg/database

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

# Database Migrations
# Use the /migrate-new, /migrate-up, or /migrate-down workflows for database changes.
# /migrate-new - Creates new migration files for all engines.
# /migrate-up - Applies all migrations and updates schema files from a clean state.
# /migrate-down - Rolls back the last migration.
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

**Redis (distributed locking):**

- Ephemeral storage in temporary directory
- Redis server on port 6379
- No authentication required (test environment)
- Used for distributed lock testing
- Self-validation checks:
  - Connection test (PING)

Configuration in `nix/process-compose/flake-module.nix` defines:

- `minio-server` process - MinIO server with health checks
- `create-buckets` process - Bucket creation and validation
- `postgres-server` process - PostgreSQL server with health checks
- `init-database` process - PostgreSQL database and user creation with validation
- `mariadb-server` process - MariaDB server with health checks
- `init-mariadb` process - MariaDB database and user creation with validation
- `redis-server` process - Redis server with health checks

The service configurations match the test environment variables to ensure consistency between dependency setup and application configuration.

### Workflows

The project defines several automated workflows in the `.agent/workflows/` directory. These are referred to as `/workflow-name` (e.g., `/migrate-new`). When performing a task that has a corresponding workflow, you MUST read the workflow file and follow its steps.

### Skills

The project uses "Skills" to provide detailed instructions and best practices for specific tools or domains. These are located in `.agent/skills/<skill-name>/SKILL.md`.

- **graphite**: Instructions for using Graphite (`gt`) for branch management and restacking.
- **dbmate**: Detailed rules and best practices for writing and applying database migrations.
- **sqlc**: Workflow for modifying database queries, generating code, and updating wrappers.

When working with these tools, you SHOULD read the corresponding `SKILL.md` to ensure compliance with project-specific rules.

### CI/CD and GitHub Actions

- **CI/CD**: GitHub Actions optimized for Graphite-style stacked PRs.
- **Auto-run Permissions**: Commands whitelisted in `.claude/settings.local.json` are pre-approved for Antigravity (SafeToAutoRun).

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

```bash
PR #7: feature-g → main          ← ✅ CI runs (only this one)
PR #6: feature-f → feature-g     ← ❌ CI skipped
PR #5: feature-e → feature-f     ← ❌ CI skipped
...
```

**When modifying workflows:** Maintain the `branches: [main]` restriction to keep CI efficient for stacked PR workflows.

## NarInfo Migration Strategy

The project supports migrating NarInfo metadata from filesystem/S3 storage to the database for improved performance and scalability. This section describes when and how to perform migrations.

### Background

Historically, ncps stored narinfo files in the storage backend (filesystem or S3). Starting with version 0.8.0, narinfo metadata is stored in the database while the actual NAR files remain in storage. This provides:

- Faster lookups (database queries vs filesystem/S3 operations)
- Better atomicity and consistency
- Support for complex queries and filtering
- Reduced storage backend load

### Migration Approaches

There are two ways narinfos are migrated to the database:

#### 1. Background Automatic Migration (Recommended for Most Cases)

**When it happens:**

- Automatically during normal operations
- Triggered when `GetNarInfo()` reads a narinfo from storage that isn't in the database yet
- Runs asynchronously in a background goroutine
- Non-blocking - doesn't delay the client response

**How it works:**

1. Client requests a narinfo via `GetNarInfo(hash)`
1. System checks database first (cache hit = fast path)
1. If not in database, checks storage backend
1. If found in storage, returns it to client immediately
1. Simultaneously spawns a background job to migrate it to the database
1. Uses distributed locks to prevent thundering herd (multiple concurrent migrations of same narinfo)
1. Optionally deletes from storage after successful migration (controlled by configuration)

**Characteristics:**

- Zero downtime
- No manual intervention required
- Gradual migration (narinfos migrate as they're accessed)
- Minimal performance impact
- Safe for production use

**Configuration:**

- Automatic migration is **always enabled** when using the database
- No special flags needed
- Migration happens transparently

#### 2. Explicit CLI Migration (For Large-Scale Migration)

**When to use:**

- Migrating a large existing cache all at once
- Before decommissioning the storage backend for narinfos
- When you want predictable migration timing
- For administrative control over the process

**Command:**

```bash
ncps migrate-narinfo \
  --cache-storage-local=/path/to/cache \
  --cache-database-url=sqlite:/path/to/db.sqlite \
  [--workers=10] \
  [--delete] \
  [--dry-run]
```

**Flags:**

- `--cache-storage-local`: Path to the cache directory (required)
- `--cache-database-url`: Database connection URL (required)
- `--workers`: Number of concurrent migration workers (default: 10, max: 50)
- `--delete`: Delete narinfos from storage after successful migration
- `--dry-run`: Show what would be migrated without actually migrating

**Process:**

1. Pre-fetches list of already-migrated hashes from database (via `GetMigratedNarInfoHashes`)
1. Walks all narinfos in the storage backend
1. Skips narinfos already in database (idempotent)
1. Migrates each narinfo in a transaction:
   - Parses narinfo from storage
   - Inserts into `narinfos` table
   - Inserts references into `narinfo_references` table
   - Inserts signatures into `narinfo_signatures` table
   - Creates/links `nar_files` record
1. Optionally deletes from storage (if `--delete-after-migration` is set)
1. Reports progress and errors

**Characteristics:**

- Idempotent (safe to run multiple times)
- Transaction-based (all-or-nothing per narinfo)
- Handles duplicate key errors gracefully
- Progress reporting
- Can run while cache is serving requests (but may cause lock contention)

**Example:**

```bash
# Dry run to see what would be migrated
ncps migrate-narinfo \
  --cache-data-path=/var/cache/ncps \
  --cache-database-url=postgresql://user:pass@localhost/ncps \
  --dry-run

# Actual migration with 20 workers
ncps migrate-narinfo \
  --cache-data-path=/var/cache/ncps \
  --cache-database-url=postgresql://user:pass@localhost/ncps \
  --workers=20

# Migration with deletion from storage
ncps migrate-narinfo \
  --cache-data-path=/var/cache/ncps \
  --cache-database-url=sqlite:/var/cache/ncps/db.sqlite \
  --workers=10 \
  --delete-after-migration
```

### Recommended Migration Strategy

For different deployment scenarios:

**New Deployment:**

- No migration needed
- All new narinfos automatically go to database
- Background migration handles any legacy data

**Small Existing Cache (\<10K narinfos):**

- Let background migration handle it automatically
- Monitor metrics to ensure migration completes
- No downtime required

**Medium Cache (10K-100K narinfos):**

- Option A: Use CLI migration during low-traffic period
- Option B: Let background migration run over time
- Consider using `--workers` flag to control load

**Large Cache (>100K narinfos):**

- Use CLI migration with careful planning:
  1. Run with `--dry-run` first to estimate scope
  1. Schedule during maintenance window or low-traffic period
  1. Start with moderate worker count (e.g., 10-20)
  1. Monitor database connection pool and performance
  1. Optionally use `--delete-after-migration` to reclaim storage space

**Multi-Node Deployment:**

- CLI migration should run on ONE node only
- Background migration works on all nodes (coordinated via distributed locks)
- Ensure database is accessible from all nodes

### Concurrent Operation Handling

The system handles several concurrent scenarios safely:

**Scenario: GetNarInfo + Background Migration (Same Hash)**

- Uses distributed locks (`TryLock`) to prevent duplicate migrations
- If lock is held, subsequent requests skip migration
- Final state: exactly one database record

**Scenario: PutNarInfo + Background Migration (Same Hash)**

- Both operations may attempt database insert
- Duplicate key errors are handled gracefully
- First to commit wins, second gets `ErrAlreadyExists`
- Final state: exactly one database record

**Scenario: CLI Migration + Background Migration (Same Hash)**

- Same as above - duplicate handling ensures consistency
- Safe to run CLI migration while serving traffic
- Idempotent operation

**Scenario: Multiple GetNarInfo Requests (Thundering Herd)**

- Only first request acquires migration lock
- Others skip migration and return from storage
- Tested in `TestGetNarInfo_BackgroundMigration_ThunderingHerd`

### Migration Verification

After migration, verify success:

```sql
-- Count narinfos in database
SELECT COUNT(*) FROM narinfos WHERE url IS NOT NULL;

-- Count narinfos in storage (using CLI)
find /path/to/cache/store/narinfo -name "*.narinfo" | wc -l

-- Check for unmigrated narinfos
SELECT COUNT(*) FROM narinfos WHERE url IS NULL;
```

### Rollback / Recovery

If migration issues occur:

1. **Stop writing to database** (requires code deployment to disable)
1. **Database has bad data**: Truncate and re-migrate:
   ```sql
   DELETE FROM narinfo_signatures;
   DELETE FROM narinfo_references;
   DELETE FROM narinfos;
   ```
1. **Storage accidentally deleted**: Restore from backup
1. **Partial migration state**: Re-run CLI migration (idempotent)

### Performance Considerations

**Database Connection Pool:**

- Increase max connections for high worker counts
- PostgreSQL: `--cache-database-url=postgresql://...?pool_max_conns=50`
- SQLite: Single-writer limitation (use lower worker count)

**Worker Count Guidelines:**

- Local filesystem storage: 10-30 workers
- S3/MinIO storage: 20-50 workers (network-bound)
- PostgreSQL database: Scale with connection pool
- SQLite database: Use lower count (5-10) due to write serialization

**Progress Monitoring:**

- Watch database size growth
- Monitor error logs for failed migrations
- Track metrics (if implemented): `narinfo_migrated_total`, `narinfo_migration_errors_total`

### Troubleshooting

**Issue: Migration is slow**

- Increase worker count (if database can handle it)
- Check database connection pool size
- Verify network latency to database
- Consider running during low-traffic period

**Issue: Duplicate key errors in logs**

- Normal during concurrent operations
- System handles gracefully

**Issue: Storage deletions failed during migration**

- Migration is idempotent - safe to re-run
- Re-run with `--delete` flag to retry deletions
- Only already-migrated narinfos will be processed
- Database migration step is skipped for already-migrated items
- Example: `ncps migrate-narinfo --delete --cache-database-url=... --cache-storage-local=...`
- If excessive, reduce worker count

**Issue: Transaction deadlocks**

- Reduce worker count
- May indicate database lock contention
- Check database isolation level settings

**Issue: Out of memory**

- `GetMigratedNarInfoHashes` loads all hashes into memory
- For very large caches (>1M narinfos), this may be problematic
- Consider pagination (future enhancement)

**Issue: Background migration not deleting from storage**

- Deletion from storage after background migration is controlled by configuration
- Check cache configuration
- Manual cleanup may be needed

### Testing

The migration system has extensive test coverage:

**Unit Tests (`pkg/ncps/migrate_narinfo_test.go`):**

- Success cases
- Idempotency
- Dry-run mode
- Delete-after-migration
- Already-migrated scenarios
- Error handling
- Concurrent migration
- Partial data (NULL fields)
- Transaction rollback

**Integration Tests (`pkg/cache/cache_test.go`):**

- Background migration during GetNarInfo
- Concurrent PutNarInfo during migration
- Multiple concurrent operations (thundering herd)
- Context cancellation handling

Run tests:

```bash
# All migration tests
go test -race -run TestMigrateNarInfo ./pkg/ncps -v

# Background migration tests
go test -race -run "TestGetNarInfo.*Migration" ./pkg/cache -v

# Concurrent operation tests
go test -race -run "TestGetNarInfo.*Concurrent" ./pkg/cache -v
```

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

- This keeps the wrapper simple and doesn't require rebuilding ncps to update it

**Creating Database Migrations:**

For ANY database changes, you MUST use one of the migration workflows:

- **/migrate-new**: To create new timestamped migration files for all engines.
- **/migrate-up**: To apply migrations and update schema files properly using `./dev-scripts/migrate-all.py`.
- **/migrate-down**: To roll back migrations.

**Implementation details:**

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
- Uses the `NCPS_DB_SCHEMA_DIR` environment variable to locate the base schema directory
  - In Docker: set to `/share/ncps/db/schema` (static path in container)
  - In dev shell: set dynamically via `shellHook` to `$(git rev-parse --show-toplevel)/db/schema` (repo root)
  - This ensures migration changes are immediately visible without rebuilding
- Automatically sets the `DBMATE_SCHEMA_FILE` environment variable to the appropriate database-specific path:
  - Example: `${NCPS_DB_SCHEMA_DIR}/sqlite.sql` or `${NCPS_DB_SCHEMA_DIR}/postgres.sql`
- Calls the real `dbmate` binary (consistently renamed to `dbmate.real` in both dev and Docker)
- Respects user overrides:
  - if `DBMATE_MIGRATIONS_DIR` is already set or `--migrations-dir` is provided, the wrapper passes through without modification
  - if `DBMATE_SCHEMA_FILE` is already set or `--schema-file` is provided, the wrapper passes through without modification
- This keeps the wrapper simple and doesn't require rebuilding ncps to update it

**IMPORTANT:** Never manually create migration files by copying existing ones, as this will result in incorrect timestamps. Always use `dbmate new` to ensure proper chronological ordering.

## Code Quality

### Linting

Strict linting via golangci-lint with 30+ linters enabled (see `.golangci.yml`). Key linters: err113, exhaustive, gosec, paralleltest, testpackage.

**IMPORTANT**: Always use `golangci-lint run --fix` first to automatically fix fixable issues before doing manual fixes. This saves tokens and is more efficient.

**Manual Fixes**:

- `testpackage`: Test files must be in the `package_test` package, even if in the same directory.
- `paralleltest`: All tests and subtests (`t.Run`) must call `t.Parallel()`. If a test relies on specific ordering and cannot be parallelized, use `//nolint:paralleltest` to document the exception. Parallel tests are highly encouraged unless absolutely impossible.
- `testifylint`: Use `require.NoError` for errors that should stop the test, and `assert` for others.
- `lll`: Break long lines (especially function calls) into multiple lines.

### Formatting

Uses gofumpt, goimports, and gci for import ordering (standard → default → alias → localmodule). SQL files are formatted using sqlfluff.

**IMPORTANT**: Always use `nix fmt` to automatically format project files (Go, Nix, etc.) before making manual edits. For SQL files specifically, use `sqlfluff format` to fix formatting issues.

### Testing

- Tests use testify for assertions
- Race detector enabled (`go test -race`)
- Test files use `_test` package suffix (testpackage linter)
- Parallel tests encouraged (paralleltest linter)

#### Integration Tests (S3, PostgreSQL, MySQL, Redis)

Integration tests are **disabled by default** and must be explicitly enabled using shell helper functions provided by the development environment. The tests are automatically skipped if the required environment variables are not set.

**For local development:**

```bash
# Start dependencies (in a separate terminal)
nix run .#deps

# Enable all integration tests using the helper command
eval "$(enable-integration-tests)"

# Run all tests including integration tests
go test -race ./...

# Or enable specific integration tests only:
eval "$(enable-s3-tests)"          # Enable S3/MinIO tests only
eval "$(enable-postgres-tests)"    # Enable PostgreSQL tests only
eval "$(enable-mysql-tests)"       # Enable MySQL tests only
eval "$(enable-redis-tests)"       # Enable Redis tests only

# Disable integration tests when done
eval "$(disable-integration-tests)"
```

**Available helper commands** (automatically available in PATH within the dev shell):

- `eval "$(enable-s3-tests)"` - Sets S3 test environment variables
- `eval "$(enable-postgres-tests)"` - Sets PostgreSQL test environment variable
- `eval "$(enable-mysql-tests)"` - Sets MySQL test environment variable
- `eval "$(enable-redis-tests)"` - Sets Redis test environment variable
- `eval "$(enable-integration-tests)"` - Enables all integration tests at once
- `eval "$(disable-integration-tests)"` - Unsets all integration test variables

These commands output export statements that you evaluate in your current shell to set the appropriate environment variables. When entering the dev shell, you'll see a message listing these available helpers.

**For Nix builds and CI:**

All integration test dependencies (MinIO, PostgreSQL, MariaDB, Redis) are automatically started during the test phase when building with Nix:

```bash
# Runs all checks including all integration tests
nix flake check

# Build package (includes test phase with all dependencies)
nix build
```

The Nix build (`nix/packages/ncps/default.nix`) automatically:

1. Starts MinIO, PostgreSQL, MariaDB, and Redis servers in the `preCheck` phase
1. Creates test databases, buckets, and credentials
1. Exports all integration test environment variables
1. Runs all tests (including all integration tests)
1. Stops all services in the `postCheck` phase

This setup ensures:

- All integration tests run in CI/CD (GitHub Actions workflows)
- `nix flake check` includes comprehensive testing across all backends
- All three database implementations (SQLite, PostgreSQL, MySQL) are tested
- S3 storage backend is tested against MinIO
- Redis distributed locks are tested for high-availability deployments
- Runtime usage (`nix run github:kalbasit/ncps`) is unaffected
- Docker builds (`.#docker`) are unaffected
- Tests are isolated and don't interfere with each other (unique hash-based keys)
- Migrations are validated against all database engines

## Configuration

Supports YAML/TOML/JSON config files. See `config.example.yaml` for all options. Key configuration areas:

- Cache settings (hostname, data-path, database-url, max-size)
- Upstream caches and public keys
- OpenTelemetry and Prometheus metrics
- Server address and security options (PUT/DELETE verb control)
