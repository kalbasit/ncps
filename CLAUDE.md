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
./dev-scripts/run.sh

# Run tests with race detector
go test -race ./...

# Run a single test
go test -race -run TestName ./pkg/package/...

# Lint code
golangci-lint run

# Generate SQL code (after modifying db/query.sql or migrations)
sqlc generate

# Run database migrations manually
dbmate --url "sqlite:/path/to/db.sqlite" up

# Build
go build .

# Build with Nix
nix build
```

## Architecture

### Package Structure

- `cmd/` - CLI commands (serve, global flags, OpenTelemetry bootstrap)
- `pkg/cache/` - Core caching logic and upstream cache fetching
- `pkg/storage/` - Storage abstraction layer with implementations:
  - `storage/local/` - Local filesystem storage
  - `storage/s3/` - S3-compatible storage (including MinIO)
- `pkg/server/` - HTTP server using Chi router
- `pkg/database/` - SQLite database layer (sqlc-generated code)
- `pkg/nar/` - NAR (Nix ARchive) format handling
- `db/migrations/` - Database migration files
- `db/query.sql` - SQL queries for sqlc code generation

### Key Interfaces (pkg/storage/store.go)

Storage uses interface-based abstraction:
- `ConfigStore` - Secret key storage
- `NarInfoStore` - NarInfo metadata storage
- `NarStore` - NAR file storage

Both local and S3 backends implement these interfaces.

### Database

SQLite with sqlc for type-safe SQL. Schema in `db/schema.sql`, queries in `db/query.sql`. Run `sqlc generate` after modifying queries.

## Code Quality

### Linting
Strict linting via golangci-lint with 30+ linters enabled (see `.golangci.yml`). Key linters: err113, exhaustive, gosec, paralleltest, testpackage.

### Formatting
Uses gofumpt, goimports, and gci for import ordering (standard → default → alias → localmodule).

### Testing
- Tests use testify for assertions
- Race detector enabled (`go test -race`)
- Test files use `_test` package suffix (testpackage linter)
- Parallel tests encouraged (paralleltest linter)

## Configuration

Supports YAML/TOML/JSON config files. See `config.example.yaml` for all options. Key configuration areas:
- Cache settings (hostname, data-path, database-url, max-size)
- Upstream caches and public keys
- OpenTelemetry and Prometheus metrics
- Server address and security options (PUT/DELETE verb control)
