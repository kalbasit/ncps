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

- go, golangci-lint, sqlfluff
- dbmate (dev-only DB reset helper), delve, watchexec
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

Start Garage, PostgreSQL, MySQL, Redis for testing:

```sh
nix run .#deps
```

This starts:

- **Garage** (S3 API on port 9000, admin API on 3903) - S3-compatible storage. Garage's upstream default S3 port is 3900; the dev setup binds 9000 to preserve the existing test-contract endpoint inherited from MinIO.
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
# Generate per-dialect migrations from the current Ent schema
task migrations:gen NAME=descriptive_snake_case

# Apply migrations
ncps migrate up --cache-database-url=sqlite:/path/to/db.sqlite
```

See [Contributing](Developer%20Guide/Contributing.md) for the full Ent + Atlas + Goose workflow.

### Ent Client Regeneration

```sh
# Regenerate the Ent client after editing ent/schema/*.go
go generate ./ent/...
```

## Testing

See <a class="reference-link" href="Developer%20Guide/Testing.md">Testing</a> for comprehensive testing information.

## Related Documentation

- <a class="reference-link" href="Developer%20Guide/Contributing.md">Contributing</a> - Full contribution guide
- <a class="reference-link" href="Developer%20Guide/Architecture.md">Architecture</a> - Deep dive into design
- <a class="reference-link" href="Developer%20Guide/Testing.md">Testing</a> - Testing procedures
