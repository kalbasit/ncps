# Contributing

## Contributing to ncps

Thank you for your interest in contributing to ncps! This document provides guidelines and instructions for contributing to the project.

## Getting Started

### Prerequisites

The project uses **Nix flakes** with **direnv** for reproducible development environments. You'll need:

1. **Nix with flakes enabled** - [Installation guide](https://nixos.org/download.html)
1. **direnv** - [Installation guide](https://direnv.net/docs/installation.html)

### Initial Setup

1. **Clone the repository:**

   ```sh
   git clone https://github.com/kalbasit/ncps.git
   cd ncps
   ```

1. **Allow direnv:**

   ```sh
   direnv allow
   ```

   This will automatically load the development environment with all required tools:

   - Go
   - golangci-lint
   - sqlc
   - dbmate
   - delve (debugger)
   - watchexec
   - sqlfluff
   - MinIO (for S3 testing)
   - PostgreSQL (for database testing)
   - MySQL/MariaDB (for database testing)
   - Redis (for distributed locking testing)

## Development Environment

### Available Tools

Once in the development shell, you have access to:

| Tool | Purpose |
| --- | --- |
| `go` | Go compiler and toolchain |
| `golangci-lint` | Code linting with 30+ linters |
| `sqlc` | Type-safe SQL code generation |
| `dbmate` | Database migration tool |
| `delve` | Go debugger |
| `watchexec` | File watcher for hot-reloading |
| `sqlfluff` | SQL linting and formatting |
| `minio` | S3-compatible object storage |
| `postgresql` | PostgreSQL database server |
| `mariadb` | MySQL/MariaDB database server |
| `redis` | Redis server for distributed locks |

### Development Dependencies

The project uses `process-compose-flake` for managing development services. Start dependencies with:

```sh
nix run .#deps
```

This starts:

- **MinIO** - S3-compatible storage server (port 9000, console on 9001)
  - Test bucket: `test-bucket`
  - Credentials: `test-access-key` / `test-secret-key`
  - Self-validation ensures proper setup
- **PostgreSQL** - Database server (port 5432)
  - Test database: `test-db`
  - Credentials: `test-user` / `test-password`
  - Connection URL: `postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable`
- **MariaDB** - MySQL-compatible database server (port 3306)
  - Test database: `test-db`
  - Credentials: `test-user` / `test-password`
  - Connection URL: `mysql://test-user:test-password@127.0.0.1:3306/test-db`
- **Redis** - Distributed locking server (port 6379)
  - No authentication required (test environment)
  - Used for distributed lock testing

## Development Workflow

### Running the Development Server

The development server supports hot-reloading and multiple storage backends:

```
# Using local filesystem storage (default, no dependencies required)
./dev-scripts/run.sh
# or explicitly
./dev-scripts/run.sh local

# Using S3/MinIO storage (requires dependencies to be running)
# In a separate terminal:
nix run .#deps

# Then run the dev server:
./dev-scripts/run.sh s3
```

The server automatically restarts when you modify code files.

### Editing Documentation

The documentation for this project is managed using Trilium. Follow these steps to contribute to the documentation:

1. **Run the documentation editor:**

   ```sh
   trilium-edit-docs
   ```

   This tool is available in the `PATH` provided by the Nix flake, see [Development Environment in the Developer Guide](../Developer%20Guide.md) for more information.

1. **Edit the documentation through Trilium:** The Trilium interface will open, allowing you to edit the notes. Trilium automatically exports the markdown files back to the repository as you make changes.

1. **Wait and close:** Wait 5 minutes after you have finished all your edits to ensure all changes are synced, then close Trilium.

1. **Format the documentation:**

   Run the project formatter to ensure the markdown files follow the project's standards:

   ```sh
   nix fmt
   ```

1. **Submit your changes:** Submit a Pull Request with your changes.

### Database Migrations

**Creating a new migration:**

```
dbmate --migrations-dir db/migrations/sqlite new migration_name
dbmate --migrations-dir db/migrations/postgres new migration_name
dbmate --migrations-dir db/migrations/mysql new migration_name
```

The `dbmate` command is a wrapper that automatically:

- Detects database type from the URL scheme
- Selects the appropriate migrations directory (`db/migrations/sqlite/`, `db/migrations/postgres/`, or `db/migrations/mysql/`)
- Creates timestamped migration files

**Applying migrations:**

```
dbmate --url "sqlite:path/to/db.sqlite" up
dbmate --url "postgresql://..." up
dbmate --url "mysql://..." up
```

**Note:** The wrapper uses the `NCPS_DB_MIGRATIONS_DIR` and the `NCPS_DB_SCHEMA_DIR` environment variables (automatically set in the dev shell) to locate migrations and schema.

### Generating SQL Code

After modifying SQL queries or migrations:

```
sqlc generate
```

This generates type-safe Go code from:

- `db/query.sqlite.sql` → `pkg/database/sqlitedb/`
- `db/query.postgres.sql` → `pkg/database/postgresdb/`
- `db/query.mysql.sql` → `pkg/database/mysqldb/`

## Code Quality Standards

### Formatting

**IMPORTANT:** Always run formatters first before making manual changes:

```
# Format all files (Go, Nix, SQL, YAML, Markdown)
nix fmt
```

The project uses:

- **gofumpt** - Stricter Go formatting
- **goimports** - Import organization
- **gci** - Import grouping (standard → default → alias → localmodule)
- **nixfmt** - Nix code formatting
- **sqlfluff** - SQL formatting and linting
- **yamlfmt** - YAML formatting
- **mdformat** - Markdown formatting

### Linting

**IMPORTANT:** Always run `golangci-lint run --fix` first to automatically fix issues:

```
# Auto-fix all fixable linting issues
golangci-lint run --fix

# Lint without auto-fix
golangci-lint run

# Lint specific package
golangci-lint run ./pkg/server/...
```

The project uses 30+ linters including:

- **err113** - Explicit error wrapping
- **exhaustive** - Exhaustive switch statements
- **gosec** - Security checks
- **paralleltest** - Parallel test detection
- **testpackage** - Test package naming

See `.golangci.yml` for complete linter configuration.

### SQL Linting

```
# Lint SQL files
sqlfluff lint db/migrations/sqlite/*.sql
sqlfluff lint db/migrations/postgres/*.sql
sqlfluff lint db/migrations/mysql/*.sql

# Format SQL files
sqlfluff format db/migrations/sqlite/*.sql
```

**Note:** sqlc query files (`db/query.*.sql`) are excluded from sqlfluff as they use sqlc-specific syntax.

## Testing

### Running Tests

```
# Run all tests with race detector (recommended)
go test -race ./...

# Run tests for specific package
go test -race ./pkg/server/...

# Run a single test
go test -race -run TestName ./pkg/server/...
```

### Integration Tests

The project includes integration tests for S3, PostgreSQL, MySQL, and Redis. Integration tests are **disabled by default** and must be explicitly enabled using shell helper functions.

**Quick Start:**

```
# In terminal 1: Start development dependencies
nix run .#deps

# In terminal 2: Enable integration tests and run tests
eval "$(enable-integration-tests)"
go test -race ./...
```

**Available Helper Commands:**

The development shell provides commands to easily enable/disable integration tests:

| Command | Description |
| --- | --- |
| `eval "$(enable-s3-tests)"` | Enable S3/MinIO integration tests |
| `eval "$(enable-postgres-tests)"` | Enable PostgreSQL integration tests |
| `eval "$(enable-redis-tests)"` | Enable Redis integration tests |
| `eval "$(enable-mysql-tests)"` | Enable MySQL integration tests |
| `eval "$(enable-integration-tests)"` | Enable all integration tests at once |
| `eval "$(disable-integration-tests)"` | Disable all integration tests |

**Running Specific Integration Tests:**

```
# Start dependencies (in a separate terminal)
nix run .#deps

# Enable and run S3 tests only
eval "$(enable-s3-tests)"
go test -race ./pkg/storage/s3

# Enable and run database tests only
eval "$(enable-postgres-tests)"
eval "$(enable-mysql-tests)"
go test -race ./pkg/database

# Enable all tests and run everything
eval "$(enable-integration-tests)"
go test -race ./...

# Disable integration tests when done
eval "$(disable-integration-tests)"
```

**What the Helper Commands Do:**

The helper commands output shell export statements that you evaluate in your current shell:

- `**enable-s3-tests**` exports:
  - `NCPS_TEST_S3_BUCKET=test-bucket`
  - `NCPS_TEST_S3_ENDPOINT=http://127.0.0.1:9000`
  - `NCPS_TEST_S3_REGION=us-east-1`
  - `NCPS_TEST_S3_ACCESS_KEY_ID=test-access-key`
  - `NCPS_TEST_S3_SECRET_ACCESS_KEY=test-secret-key`
- `**enable-postgres-tests**` exports:
  - `NCPS_TEST_POSTGRES_URL=postgresql://test-user:test-password@127.0.0.1:5432/test-db?sslmode=disable`
- `**enable-mysql-tests**` exports:
  - `NCPS_TEST_MYSQL_URL=mysql://test-user:test-password@127.0.0.1:3306/test-db`
- `**enable-redis-tests**` exports:
  - `NCPS_ENABLE_REDIS_TESTS=1`

Tests automatically skip if these environment variables aren't set, so you can run `go test -race ./...` without enabling integration tests and only unit tests will run.

### Test Requirements

- Use **testify** for assertions
- Enable race detector (`-race` flag)
- Use `_test` package suffix (enforced by `testpackage` linter)
- Write parallel tests where possible (checked by `paralleltest` linter)
- Each test should be isolated and not depend on other tests

### Nix Build Tests

```
# Run all checks including integration tests
nix flake check

# Build package (includes test phase)
nix build
```

The Nix build automatically:

1. Starts MinIO, PostgreSQL, MariaDB, and Redis in `preCheck` phase
1. Creates test databases and buckets
1. Exports test environment variables
1. Runs all tests (including integration tests)
1. Stops services in `postCheck` phase

### Helm Chart Testing

The project includes comprehensive Helm chart testing using a local Kind cluster with MinIO, PostgreSQL, MariaDB, and Redis. A unified CLI tool (`k8s-tests`) manages the complete testing workflow.

**Prerequisites:**

All tools are provided by the Nix development environment:

- Docker (for Kind)
- kubectl, helm, kind (via Nix)
- k8s-tests CLI (automatically available in `nix develop`)

**Quick Start (Complete Workflow):**

```sh
# Run all 5 steps in one command
k8s-tests all
```

This command:

1. Creates Kind cluster with all dependencies
1. Builds and pushes Docker image
1. Generates 13 test values files
1. Installs all test deployments
1. Runs comprehensive tests

**Individual Steps:**

```sh
# 1. Create Kind cluster with all dependencies (MinIO, PostgreSQL, MariaDB, Redis)
k8s-tests cluster create

# 2. Build Docker image, push to local registry, and generate test values
k8s-tests generate --push

# 3. Install all test deployments
k8s-tests install

# 4. Run tests across all deployments
k8s-tests test

# 5. Cleanup when done
k8s-tests cleanup
```

The cluster can be reused across test runs. Use `k8s-tests cluster destroy` to clean up when done.

**Alternative: Use External Image**

If you've already built and pushed an image:

```sh
# Use a specific tag
k8s-tests generate sha-cf09394

# Use custom registry and repository
k8s-tests generate 0.5.1 docker.io kalbasit/ncps

# Or build externally and use the tag
DOCKER_IMAGE_TAGS="yourregistry.com/ncps:sha$(git rev-parse --short HEAD)" nix run .#push-docker-image
k8s-tests generate sha$(git rev-parse --short HEAD)-x86_64-linux yourregistry.com ncps
```

**Test Permutations:**

The tool generates 13 different test configurations:

- **Single Instance (7 scenarios):**
  - Local storage: SQLite, PostgreSQL, MariaDB
  - S3 storage: SQLite, PostgreSQL, MariaDB
  - S3 + PostgreSQL + CDC enabled
- **External Secrets (2 scenarios):**
  - S3 + PostgreSQL/MariaDB with existing Kubernetes secrets
- **High Availability (4 scenarios):**
  - 2 replicas with S3 storage
  - PostgreSQL or MariaDB database
  - Redis locks
  - With/without CDC enabled

**Testing Individual Deployments:**

```sh
# Install a single deployment
k8s-tests install single-local-sqlite

# Test specific deployment (verbose)
k8s-tests test single-local-sqlite -v

# Test specific deployment (non-verbose)
k8s-tests test single-s3-postgres

# Cleanup specific deployment
k8s-tests cleanup single-local-sqlite
```

**Manual Installation:**

```sh
# Install individual deployment manually using generated values
helm upgrade --install ncps-single-local-postgres charts/ncps \
  -f charts/ncps/test-values/single-local-postgres.yaml \
  --create-namespace \
  --namespace ncps-single-local-postgres
```

**Cluster Management:**

```sh
# Show cluster connection information
k8s-tests cluster info

# Destroy cluster
k8s-tests cluster destroy

# Help
k8s-tests --help
```

**Adding New Test Scenarios:**

To add a custom test permutation, edit `nix/k8s-tests/config.nix`:

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

## Pull Request Process

### Before Submitting

1. **Format your code:**

   ```
   nix fmt
   ```

1. **Fix linting issues:**

   ```
   golangci-lint run --fix
   ```

1. **Run tests:**

   ```
   go test -race ./...
   ```

1. **Build successfully:**

   ```
   nix build
   ```

### Commit Guidelines

- Use clear, descriptive commit messages
- Follow [Conventional Commits](https://www.conventionalcommits.org/) when possible:
  - `feat:` - New features
  - `fix:` - Bug fixes
  - `docs:` - Documentation changes
  - `refactor:` - Code refactoring
  - `test:` - Test additions/changes
  - `chore:` - Build/tooling changes

### Pull Request Guidelines

1. **Create a feature branch:**

   ```
   git checkout -b feature/your-feature-name
   ```

1. **Make your changes** following code quality standards

1. **Update documentation** if needed (README.md, CLAUDE.md, etc.)

1. **Add tests** for new functionality

1. **Submit PR** with:

   - Clear description of changes
   - Reference to related issues
   - Screenshots/examples if applicable

### CI/CD Notes

The project uses GitHub Actions for CI/CD:

- Workflows only run on PRs targeting `main` branch
- This supports Graphite-style stacked PRs efficiently
- When modifying workflows, maintain the `branches: [main]` restriction

## Project Structure

```
ncps/
├── cmd/                        # CLI commands
│   └── serve.go                # Main serve command
├── pkg/
│   ├── cache/                  # Core caching logic
│   ├── storage/                # Storage abstraction
│   │   ├── local/              # Local filesystem storage
│   │   └── s3/                 # S3-compatible storage
│   ├── database/               # Database abstraction
│   │   ├── sqlitedb/           # SQLite implementation
│   │   ├── postgresdb/         # PostgreSQL implementation
│   │   └── mysqldb/            # MySQL/MariaDB implementation
│   ├── lock/                   # Lock abstraction
│   │   ├── local/              # Local locks (single-instance)
│   │   └── redis/              # Redis distributed locks (HA)
│   ├── server/                 # HTTP server (Chi router)
│   └── nar/                    # NAR format handling
├── db/
│   ├── migrations/             # Database migrations
│   │   ├── sqlite/             # SQLite migrations
│   │   ├── postgres/           # PostgreSQL migrations
│   │   └── mysql/              # MySQL migrations
│   ├── query.sqlite.sql        # SQLite queries (sqlc)
│   ├── query.postgres.sql      # PostgreSQL queries (sqlc)
│   └── query.mysql.sql         # MySQL queries (sqlc)
├── nix/                        # Nix configuration
│   ├── packages/               # Package definitions
│   ├── devshells/              # Development shells
│   ├── formatter/              # Formatter configuration
│   ├── process-compose/        # Development services
│   └── dbmate-wrapper/         # Database migration wrapper
└── dev-scripts/                # Development helper scripts
    └── run.sh                  # Development server script
```

### Key Interfaces

**Storage (**`**pkg/storage/store.go**`**):**

- `ConfigStore` - Secret key storage
- `NarInfoStore` - NarInfo metadata storage
- `NarStore` - NAR file storage

Both local and S3 backends implement these interfaces.

**Locks (**`**pkg/lock/lock.go**`**):**

- `Locker` - Exclusive locking for download deduplication
- `RWLocker` - Read-write locking for LRU coordination

Both local (sync.Mutex) and Redis (Redlock) backends implement these interfaces. Redis locks enable high-availability deployments with multiple instances.

**Database:**

- Supports SQLite, PostgreSQL, and MySQL via sqlc
- Database selection via URL scheme in `--cache-database-url`
- Type-safe queries generated from `db/query.*.sql` files

## Common Development Tasks

### Adding a New Database Migration

```
# Create migration for all databases
dbmate --migrations-dir db/migrations/sqlite new add_new_feature
dbmate --migrations-dir db/migrations/postgres new add_new_feature
dbmate --migrations-dir db/migrations/mysql new add_new_feature

# Edit the migration files in:
# - db/migrations/sqlite/TIMESTAMP_add_new_feature.sql
# - db/migrations/postgres/TIMESTAMP_add_new_feature.sql
# - db/migrations/mysql/TIMESTAMP_add_new_feature.sql

# IMPORTANT: Do NOT wrap migrations in BEGIN/COMMIT blocks
# dbmate automatically wraps each migration in a transaction
# Adding manual transactions will cause "cannot start a transaction within a transaction" errors
#
# Example migration:
# -- migrate:up
# CREATE TABLE example (...);
# CREATE INDEX idx_example ON example (column);
#
# -- migrate:down
# DROP INDEX idx_example;
# DROP TABLE example;

# Test the migration
dbmate --url "sqlite:./test.db" up
dbmate --url "postgresql://..." up
dbmate --url "mysql://..." up
```

### Adding New SQL Queries

Edit the appropriate query file:

- db/query.sqlite.sql (SQLite-specific queries)
- db/query.postgres.sql (PostgreSQL-specific queries)
- db/query.mysql.sql (MySQL-specific queries)

### Generating SQL Code

After modifying SQL queries or migrations:

```
sqlc generate
go generate ./pkg/database
```

This generates type-safe Go code from:

- `db/query.sqlite.sql` → `pkg/database/sqlitedb/`
- `db/query.postgres.sql` → `pkg/database/postgresdb/`
- `db/query.mysql.sql` → `pkg/database/mysqldb/`

And automatically updates:

- `pkg/database/querier.go` (Common interface)
- `pkg/database/models.go` (Common models)
- `pkg/database/wrapper_*.go` (Engine adaptors)

### Adding a New Storage Backend

1. Implement the storage interfaces in `pkg/storage/`
1. Add configuration flags in `cmd/serve.go`
1. Update documentation in README.md and CLAUDE.md
1. Add integration tests
1. Update Docker and Kubernetes examples if applicable

### Debugging

Use delve for debugging:

```
# Debug the application
dlv debug . -- serve --cache-hostname=localhost --cache-storage-local=/tmp/ncps

# Debug a specific test
dlv test ./pkg/server -- -test.run TestName
```

**Note:** The dev shell disables fortify hardening to allow delve to work.

### Building Docker Images

```
# Build Docker image
nix build .#docker

# Load into Docker
docker load < result

# Push to registry (requires DOCKER_IMAGE_TAGS environment variable)
DOCKER_IMAGE_TAGS="kalbasit/ncps:latest kalbasit/ncps:v1.0.0" nix run .#push-docker-image
```

## Getting Help

- **Documentation Issues:** Check CLAUDE.md for detailed development guidance
- **Bug Reports:** [Open an issue](https://github.com/kalbasit/ncps/issues)
- **Questions:** [Start a discussion](https://github.com/kalbasit/ncps/discussions)
- **Security Issues:** Contact maintainers privately

## Code of Conduct

- Be respectful and inclusive
- Provide constructive feedback
- Focus on what's best for the project
- Show empathy towards other contributors

## License

By contributing to ncps, you agree that your contributions will be licensed under the MIT License.

______________________________________________________________________

Thank you for contributing to ncps! 🎉
