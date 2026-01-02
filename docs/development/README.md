[Home](../../README.md) > [Documentation](../README.md) > Development

# Development Guide

Information for contributing to ncps development.

## Quick Start

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for comprehensive development guidelines.

## Development Environment

ncps uses Nix flakes with direnv for development environment:

```bash
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

```bash
# Local storage (default)
./dev-scripts/run.sh

# S3 storage (requires dependencies)
nix run .#deps  # In separate terminal
./dev-scripts/run.sh s3
```

### Development Dependencies

Start MinIO, PostgreSQL, MySQL, Redis for testing:

```bash
nix run .#deps
```

This starts:

- **MinIO** (port 9000) - S3-compatible storage
- **PostgreSQL** (port 5432) - Database
- **MySQL/MariaDB** (port 3306) - Database
- **Redis** (port 6379) - Distributed locks

### Code Quality

```bash
# Format code
nix fmt

# Lint with auto-fix
golangci-lint run --fix

# Run tests
go test -race ./...
```

### Database Migrations

```bash
# Create new migration
dbmate new migration_name

# Run migrations
dbmate migrate up
```

### SQL Code Generation

```bash
# After modifying db/query.sql
sqlc generate
```

## Testing

See [Testing Guide](testing.md) for comprehensive testing information.

## Related Documentation

- [CONTRIBUTING.md](../../CONTRIBUTING.md) - Full contribution guide
- [CLAUDE.md](../../CLAUDE.md) - Development environment guide
- [Testing Guide](testing.md) - Testing procedures
