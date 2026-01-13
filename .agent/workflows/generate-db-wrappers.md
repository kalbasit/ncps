---
description: Generate database wrappers for all supported database engines
---

# Generate Database Wrappers

This workflow guides you through generating the database wrappers for SQLite, PostgreSQL, and MySQL. These wrappers provide a common interface for different database backends.

## Prerequisites

- Go installed and configured.
- `sqlc` installed (if SQL queries change).
- The `gen-db-wrappers` tool must be available in your PATH or via `go run`.
- **Nix environment**: This project uses Nix for its development environment. The `gen-db-wrappers` tool is available in the Nix shell. You should run the following steps within `nix develop` or after loading `direnv`.

## Workflow Steps

### 1. Generate Wrappers

Run the `go generate` command specifically for the `pkg/database` package. If you are not inside a Nix shell, use `nix develop --command`.

// turbo

```bash
nix develop --command go generate ./pkg/database
```

### 2. Verify Generated Files

Ensure that the following files have been updated:

- `pkg/database/wrapper_sqlite.go`
- `pkg/database/wrapper_postgres.go`
- `pkg/database/wrapper_mysql.go`

### 3. Lint and Format

After generating the wrappers, run the linting workflow to ensure the generated code is properly formatted and passes quality checks.

// turbo

```bash
nix develop --command golangci-lint run --fix
nix fmt
```

> [!IMPORTANT]
> Do NOT edit these files manually. They are overwritten every time the generation tool is run. If you need to fix lint errors or change the logic, modify the generation tool or the `querier.go` interface.
