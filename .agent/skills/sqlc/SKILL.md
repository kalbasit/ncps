---
name: sqlc
description: Working with sqlc and database queries
---

# SQLC Skill

This skill provides instructions for working with `sqlc` and database queries in the NCPS repository. NCPS supports multiple database engines (SQLite, PostgreSQL, MySQL), and `sqlc` is used to generate type-safe Go code from SQL queries for each engine.

## Configuration

- **SQLC Config**: `sqlc.yml`
- **Queries**:
  - SQLite: `db/query.sqlite.sql`
  - PostgreSQL: `db/query.postgres.sql`
  - MySQL: `db/query.mysql.sql`
- **Output**:
  - SQLite: `pkg/database/sqlitedb`
  - PostgreSQL: `pkg/database/postgresdb`
  - MySQL: `pkg/database/mysqldb`

## Workflow for Query Changes

Any time a query file (`db/query.<engine>.sql`) is updated, you MUST follow these steps:

### 1. Generate SQLC Code

Run the `sqlc generate` command to update the generated Go files for all engines.

```bash
sqlc generate
```

### 2. Update the Querier Interface

If a function in the SQL file is **added, updated, or deleted**, you MUST update the `Querier` interface in `pkg/database/querier.go`.

The `Querier` interface defines the common methods that all database implementations must satisfy.

> [!IMPORTANT]
> Ensure the method signature in `querier.go` matches the generated signatures in the engine-specific packages.

### 3. Regenerate Database Wrappers

After updating `querier.go`, you MUST run `go generate` for the `pkg/database` package to update the database wrappers (`wrapper_sqlite.go`, `wrapper_postgres.go`, `wrapper_mysql.go`).

```bash
go generate ./pkg/database
```

The database wrappers are generated using a custom tool `gen-db-wrappers` (located in `nix/gen-db-wrappers/`) which uses the `Querier` interface as the source of truth.

## Best Practices

- **Consistency**: Ensure that equivalent queries exist for all supported engines unless the feature is engine-specific.
- **Linting**: Use `sqlfluff` to lint and format SQL files before running `sqlc generate`.

  ```bash
  sqlfluff lint db/query.*.sql
  sqlfluff format db/query.*.sql
  ```
- **Domain Structs**: The `gen-db-wrappers` tool has special handling for domain structs (e.g., `NarInfo`, `NarFile`). If you add new structs to the models, ensure they are handled correctly by the wrapper generation.
