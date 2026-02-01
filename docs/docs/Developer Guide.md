# Developer Guide

## Development Guide

Information for contributing to ncps development.

## Quick Start

See <a class="reference-link" href="Developer%20Guide/Contributing.md">Contributing</a> for comprehensive development guidelines.

## Development Environment

ncps uses Nix flakes with direnv for development environment:

```sh
# Clone repository
git clone https://github.com/kalbasit/ncps.git
cd ncps

# Enter dev shell (if using direnv)
direnv allow

# Or manually
nix develop
```

**Available tools in dev shell:**

- go, golangci-lint, sqlc, sqlfluff
- dbmate, delve, watchexec
- Integration test helpers

## Development Workflow

### Run Development Server

```sh
# Local storage (default)
./dev-scripts/run.py

# S3 storage (requires dependencies)
nix run .#deps  # In separate terminal
./dev-scripts/run.py --mode ha --locker redis

# Other modes and flags
./dev-scripts/run.py --help
```

### Development Dependencies

Start MinIO, PostgreSQL, MySQL, Redis for testing:

```sh
nix run .#deps
```

This starts:

- **MinIO** (port 9000) - S3-compatible storage
- **PostgreSQL** (port 5432) - Database
- **MySQL/MariaDB** (port 3306) - Database
- **Redis** (port 6379) - Distributed locks

### Code Quality

```sh
# Format code
nix fmt

# Lint with auto-fix
golangci-lint run --fix

# Run tests
go test -race ./...
```

### Database Migrations

```sh
# Create new migration
dbmate --migrations-dir db/migrations/sqlite new migration_name

# Run migrations
dbmate migrate up
```

### SQL Code Generation

```sh
# After modifying db/query.sql
sqlc generate
```

## Testing

See <a class="reference-link" href="Developer%20Guide/Testing.md">Testing</a> for comprehensive testing information.

## Related Documentation

- <a class="reference-link" href="Developer%20Guide/Contributing.md">Contributing</a> - Full contribution guide
- <a class="reference-link" href="Developer%20Guide/Architecture.md">Architecture</a> - Deep dive into design
- <a class="reference-link" href="Developer%20Guide/Testing.md">Testing</a> - Testing procedures
