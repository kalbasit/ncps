# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

ncps (Nix Cache Proxy Server) is a Go application that acts as a local binary cache proxy for Nix. It fetches store paths from upstream caches (like cache.nixos.org) and caches them locally, reducing download times and bandwidth usage.

## Development Commands

### Prerequisites

Uses Nix flakes with direnv (`.envrc` with `use_flake`). Tools available in dev shell: go, go-task, golangci-lint, sqlfluff, delve, watchexec.

### Common Commands

```bash
# Run development server (hot-reload with watchexec)
./dev-scripts/run.sh              # Uses local filesystem storage (default)
./dev-scripts/run.sh local        # Explicitly use local storage
./dev-scripts/run.sh s3           # Use S3/Garage storage (requires deps to be running)

# Start development dependencies (Garage for S3 testing, PostgreSQL for database testing)
nix run .#deps                    # Starts Garage and PostgreSQL with self-validation

# Run tests with race detector
go test -race ./...

# Run a single test
go test -race -run TestName ./pkg/server/...

# Lint code
golangci-lint run
golangci-lint run --fix  # Automatically fix fixable linter issues

# Format code
nix fmt                  # Format all project files (Go, Nix, SQL, etc.)

# Regenerate Ent client (after editing ent/schema/*.go)
go generate ./ent/...                      # Or: task ent:generate

# Lint Ent schemas + generated baseline migrations for codegen invariants
go run ./cmd/ent-lint --root .             # Or: task ent:lint

# Verify Ent code is up to date AND lints cleanly
task ent:check

# Generate per-dialect Atlas migrations (one .sql per dialect, shared timestamp)
go run ./cmd/generate-migrations --name=descriptive_snake_case
# Or: task migrations:gen NAME=descriptive_snake_case

# Generate empty Goose stubs (for backfills + the four-step NOT NULL recipe)
go run ./cmd/generate-migrations --sql-only --name=descriptive_snake_case
# Or: task migrations:sql NAME=descriptive_snake_case

# Apply migrations (embedded; runs goose against the right dialect)
ncps migrate up --cache-database-url=sqlite:/path/to/db.sqlite
ncps migrate up --cache-database-url=postgresql://user:password@host:port/database
ncps migrate up --cache-database-url=mysql://user:password@host:port/database

# Preview pending migrations without touching the database
ncps migrate up --dry-run --cache-database-url=sqlite:/path/to/db.sqlite

# Verify the atlas.sum integrity files are current
go run ./cmd/atlas-sum-check --root .

# Build
go build .

# Build with Nix
nix build

# Database Migrations
# Use the /migrate-new and /migrate-up skills for database changes.
# /migrate-new - Edits an Ent schema + generates per-dialect Atlas migrations.
# /migrate-up  - Applies migrations via `ncps migrate up` (with --dry-run preview).
# /migrate-down - Documents the expand-contract policy (migrations are forward-only).
```

## Development Workflow

### Storage Backends

The development server (`./dev-scripts/run.sh`) supports two storage backends:

**Local Storage (default):**

- No external dependencies required
- Uses temporary directory for cache storage
- Ideal for quick testing and development
- Storage is ephemeral (cleaned up on script exit)

**S3 Storage (Garage):**

- Requires running Garage via `nix run .#deps`
- Tests S3-compatible storage implementation
- Uses Garage server on `127.0.0.1:9000`
- Pre-configured with test credentials and bucket
- Includes self-validation to ensure proper setup

### Dependency Management (process-compose)

The project uses [process-compose-flake](https://github.com/Platonic-Systems/process-compose-flake) for managing development dependencies. Currently provides:

**`nix run .#deps`** - Starts development services:

**Garage (S3-compatible storage):**

- Ephemeral storage in temporary directory
- Garage S3 API on port 9000 (no separate web console — use `garage` CLI instead)
- Garage admin API on port 3903 (health checks, metrics)
- Pre-configured test bucket (`test-bucket`)
- Test credentials: `GK1234567890abcdef12345678` / `0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`
- Self-validation checks (via `awscli2`):
  - Signed put/get round-trip
  - Public access blocking (anonymous GET is rejected)
  - Presigned URL generation and access

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

- `garage-server` process - Garage server with health checks
- `garage-init` process - Layout assignment, bucket + key creation, smoke test
- `postgres-server` process - PostgreSQL server with health checks
- `init-database` process - PostgreSQL database and user creation with validation
- `mariadb-server` process - MariaDB server with health checks
- `init-mariadb` process - MariaDB database and user creation with validation
- `redis-server` process - Redis server with health checks

The service configurations match the test environment variables to ensure consistency between dependency setup and application configuration.

### Skills

The project uses "Skills" to provide detailed instructions and best practices for specific tools or domains. These are located in `.agent/skills/<skill-name>/SKILL.md`.

- **graphite**: Instructions for using Graphite (`gt`) for branch management and restacking.
- **ent-schema**: Rules for editing `ent/schema/*.go` — the five codegen invariants enforced by `cmd/ent-lint` and the snake_case enum-type convention.
- **migrate-new**: Workflow for editing an Ent schema and generating per-dialect Atlas migrations.
- **migrate-up**: Workflow for applying migrations via `ncps migrate up`.
- **migrate-down**: Documents the expand-contract policy — migrations are forward-only; column changes follow the four-step recipe.

When working with these tools, you SHOULD read the corresponding `SKILL.md` to ensure compliance with project-specific rules.

### Helm Chart Testing

The Helm chart includes comprehensive unit tests using [helm-unittest](https://github.com/helm-unittest/helm-unittest).

**Run tests locally:**

```bash
# Install helm-unittest plugin (first time only)
helm plugin install https://github.com/helm-unittest/helm-unittest

# Run all tests
helm unittest charts/ncps

# Run specific test file
helm unittest charts/ncps -f tests/validation_test.yaml

# Run with verbose output
helm unittest charts/ncps -3

# Run tests in Nix (includes all checks)
nix flake check
```

**Test coverage:**

- ConfigMap rendering and CDC formatting (ensures integers, not exponential notation)
- Secret generation and database URL construction (PostgreSQL/MySQL with/without passwords)
- Chart validation logic (HA requirements, storage, database, etc.)
- Deployment and StatefulSet rendering
- All values from `values.yaml`

**Add new tests:**

Create or edit test files in `charts/ncps/tests/`. Test files must end with `_test.yaml`.

See `charts/ncps/tests/README.md` for detailed instructions and best practices.

**CI integration:**

Helm tests are automatically run in CI via `nix flake check`, which includes the `helm-unittest-check` in the checks output. The `build` job in `.github/workflows/ci.yml` calls `nix flake check`, ensuring all Helm tests pass before merging.

### Kind Integration Tests

The project includes comprehensive Kubernetes integration testing using a local Kind cluster. A unified CLI tool (`k8s-tests`) tests 12 different deployment permutations across multiple storage backends, database engines, and high-availability configurations.

**Quick Start:**

```bash
# Complete workflow (all 5 steps in one command)
k8s-tests all

# Or run individual steps:
k8s-tests cluster create     # 1. Create Kind cluster with dependencies
k8s-tests generate --push    # 2. Build & push image, generate values
k8s-tests install            # 3. Deploy all 12 test scenarios
k8s-tests test               # 4. Run comprehensive tests
k8s-tests cleanup            # 5. Remove test deployments
```

**Test Permutations (12 scenarios):**

- **Single Instance (7)**: Local/S3 storage × SQLite/PostgreSQL/MariaDB, plus CDC variant
- **External Secrets (2)**: S3 + PostgreSQL/MariaDB with existing Kubernetes secrets
- **High Availability (3)**: 2 replicas with S3, databases, and Redis locks, plus CDC variant

**Working with Specific Deployments:**

```bash
# Install and test a single scenario
k8s-tests install single-local-sqlite
k8s-tests test single-local-sqlite -v
k8s-tests cleanup single-local-sqlite

# Use external image (instead of building)
k8s-tests generate sha-cf09394
k8s-tests generate 0.5.1 docker.io kalbasit/ncps

# Cluster management
k8s-tests cluster info       # Show connection credentials
k8s-tests cluster destroy    # Remove Kind cluster
```

**Architecture:**

- **Configuration**: Declarative Nix attribute sets in `nix/k8s-tests/config.nix`
- **Generation**: Shell functions + heredocs for template rendering
- **Packaging**: Nix `writeShellApplication` with managed dependencies
- **Integration**: Automatically available in dev shell PATH

**Adding New Permutations:**

Edit `nix/k8s-tests/config.nix` to add a new test scenario:

```nix
{
  permutations = [
    # ... existing permutations
    {
      name = "my-new-scenario";
      description = "My custom test scenario";
      replicas = 1;
      storage = { type = "s3"; };
      database = { type = "postgresql"; };
      redis.enabled = false;
      features = [];  # Optional: ["cdc", "ha", "pod-disruption-budget"]
    }
  ];
}
```

Then regenerate: `k8s-tests generate --push`

**For more details:** See `nix/k8s-tests/README.md`

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
- S3/Garage storage: 20-50 workers (network-bound)
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
- `cmd/ent-lint/` - AST-based linter that enforces the Ent codegen invariants
- `cmd/generate-migrations/` - Atlas-driven per-dialect migration generator
- `cmd/atlas-sum-check/` - CI helper that verifies every `atlas.sum` is current
- `pkg/cache/` - Core caching logic and upstream cache fetching
- `pkg/storage/` - Storage abstraction layer with implementations:
  - `storage/local/` - Local filesystem storage
  - `storage/s3/` - S3-compatible storage (e.g., Garage, AWS S3, Ceph)
- `pkg/server/` - HTTP server using Chi router
- `pkg/database/` - Thin facade over the generated Ent client (`*database.Client` wraps `*ent.Client` + driver metadata)
  - `pkg/database/migrate/` - State detection + adoption + apply path for `ncps migrate up`
- `pkg/nar/` - NAR (Nix ARchive) format handling
- `ent/schema/` - Hand-authored Ent schemas (the only DDL source of truth)
- `ent/` - Generated Ent client (committed; produced by `go generate ./ent/...`)
- `migrations/` - Goose-formatted Atlas migrations + integrity sums (embedded into the binary)
  - `migrations/sqlite/` - SQLite migration files + `atlas.sum`
  - `migrations/postgres/` - PostgreSQL migration files + `atlas.sum`
  - `migrations/mysql/` - MySQL/MariaDB migration files + `atlas.sum`

### Key Interfaces (pkg/storage/store.go)

Storage uses interface-based abstraction:

- `ConfigStore` - Secret key storage
- `NarInfoStore` - NarInfo metadata storage
- `NarStore` - NAR file storage

Both local and S3 backends implement these interfaces.

### Database

Supports multiple database engines via [Ent](https://entgo.io/) as the
type-safe ORM, [Atlas](https://atlasgo.io/) as the migration-diff engine
(used as a Go library, never the `atlas` CLI), and
[Goose](https://github.com/pressly/goose) as the runtime migration runner.

- **SQLite** (default): Embedded database, no external dependencies
- **PostgreSQL**: Scalable relational database for production deployments
- **MySQL/MariaDB**: Popular open-source relational database for production deployments

Database selection is done via URL scheme in the `--cache-database-url` flag:

- SQLite: `sqlite:/path/to/db.sqlite`
- PostgreSQL: `postgresql://user:password@host:port/database`
- MySQL/MariaDB: `mysql://user:password@host:port/database`

Schemas live in `ent/schema/*.go` (one file per entity). Run
`go generate ./ent/...` (or `task ent:generate`) to regenerate the Ent
client under `ent/`. The generated tree is committed and verified in CI
by the `ent-codegen-drift-check` derivation.

**Creating Database Migrations:**

For ANY database changes, follow this workflow:

1. Edit `ent/schema/<entity>.go` (field, edge, index, or annotation change).
1. Regenerate the Ent client: `go generate ./ent/...`.
1. Emit per-dialect Atlas migrations under `migrations/<dialect>/`:
   `go run ./cmd/generate-migrations --name=<descriptive_snake_case>`
1. Review the generated SQL. Each dialect file is a single timestamp-prefixed
   `.sql` with `+goose Up` / `+goose Down` markers. SQLite files that need
   `PRAGMA foreign_keys = OFF` must also carry `-- +goose NO TRANSACTION`.
1. Apply the migrations:
   `ncps migrate up --cache-database-url=<url>` (use `--dry-run` to preview).

Use the **/migrate-new** skill for the schema-edit + generate flow,
**/migrate-up** for applying, and **/migrate-down** for the expand-contract
recipe (migrations are forward-only; the `migrate down` command is
implemented but unsupported — it returns an error pointing at the
expand-contract policy).

**Expand-contract policy (never alter columns in place):**

Column changes that aren't purely additive — type changes, NOT NULL
additions, renames — must be split across multiple deploys so old and new
binaries can coexist against the same schema:

1. **Add** the new column (nullable) in migration N.
1. **Backfill** the new column in migration N+1 (SQL-only stub).
1. **Switch reads** to the new column in the application; deploy.
1. **Drop** the old column in migration N+2.

**Four-step NOT NULL recipe** (the specific case of adding a NOT NULL
constraint to an existing nullable column):

1. Migration A: ADD COLUMN nullable, default-able.
1. Migration B (sql-only stub): BACKFILL existing rows.
1. Migration C: ADD CONSTRAINT NOT NULL (or ALTER COLUMN ... SET NOT NULL).
1. Deploy each step independently; never combine in a single migration.

**Ent codegen invariants** (enforced by `cmd/ent-lint`):

- **A1** — Field-level `entsql.Check(...)` annotations are silently
  dropped by Ent. CHECKs MUST live on table-level `Annotations()`.
- **A2** — Ent uses snake_case enum-type names. Every `field.Enum(...)`
  needs a matching `entsql.Annotation{Type: "<table>_<column>_enum"}`.
- **A3** — UNIQUE columns also bound by `edge.From().Field()` must carry
  duplicate index annotations so Ent doesn't fabricate phantom indexes.
- **A4** — Every `edge.To(name, T.Type)` needs a reciprocal
  `edge.From(...,T.Type).Ref(name)` on the target schema (otherwise Ent
  fabricates a phantom FK column on the target).
- **A5** — Every `field.Bytes("*_ciphertext")` field MUST chain
  `.Sensitive()` so the ciphertext never appears in error messages, logs,
  or generated `String()` methods.

See `cmd/ent-lint/main.go` for the live AST enforcement of A1, A2, and
A4; A3 and A5 are tracked in the ent-schema-lint spec and currently
enforced by code review.

**IMPORTANT:** Never hand-edit a committed migration file. The
`atlas.sum` integrity file under each `migrations/<dialect>/` seals the
directory contents; any change must be produced by re-running
`cmd/generate-migrations`, which regenerates `atlas.sum` automatically.

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
eval "$(enable-s3-tests)"          # Enable S3/Garage tests only
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

All integration test dependencies (Garage, PostgreSQL, MariaDB, Redis) are automatically started during the test phase when building with Nix:

```bash
# Runs all checks including all integration tests
nix flake check

# Build package (includes test phase with all dependencies)
nix build
```

The Nix build (`nix/packages/ncps/default.nix`) automatically:

1. Starts Garage, PostgreSQL, MariaDB, and Redis servers in the `preCheck` phase
1. Creates test databases, buckets, and credentials
1. Exports all integration test environment variables
1. Runs all tests (including all integration tests)
1. Stops all services in the `postCheck` phase

This setup ensures:

- All integration tests run in CI/CD (GitHub Actions workflows)
- `nix flake check` includes comprehensive testing across all backends
- All three database implementations (SQLite, PostgreSQL, MySQL) are tested
- S3 storage backend is tested against Garage
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

## Container use

ALWAYS use ONLY Environments for ANY and ALL file, code, or shell operations—NO EXCEPTIONS—even for simple or generic requests.

DO NOT install or use the git cli with the environment_run_cmd tool. All environment tools will handle git operations for you. Changing ".git" yourself will compromise the integrity of your environment.

You MUST inform the user how to view your work using `container-use log <env_id>` AND `container-use checkout <env_id>`. Failure to do this will make your work inaccessible to others.

### Setting up / updating the dev image

Run the following from the repo root to build (if needed) and configure the container-use base image:

```bash
nix run .#update-cu-base
```

This script:

1. Builds `nix build .#packages.<linux-system>.docker-dev` (instant if already cached by nix)
1. Loads the image — `buildLayeredImage` with `tag = null` automatically tags it with the nix output hash
1. Checks Docker Hub via `skopeo inspect` — skips push if the image already exists
1. Updates `.container-use/environment.json` with the new `base_image` reference

Run this whenever `nix/packages/docker-dev.nix` or `nix/dev-packages.nix` changes, or after switching branches.
