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

### 2. Regenerate Database Wrappers and Models

Run `go generate` for the `pkg/database` package. This command uses `gen-db-wrappers` to automatically:
1. Extract the `Querier` interface from the `postgresdb` backend.
2. Generate the common `Querier` interface in `pkg/database/querier.go`.
3. Generate common domain models in `pkg/database/models.go`.
4. Generate database wrappers (`wrapper_sqlite.go`, `wrapper_postgres.go`, `wrapper_mysql.go`).

```bash
go generate ./pkg/database
```

> [!IMPORTANT]
> Do NOT manually edit `pkg/database/querier.go` or `pkg/database/models.go`. They are fully automated.

## Best Practices

- **Consistency**: Ensure that equivalent queries exist for all supported engines unless the feature is engine-specific.
- **Linting**: Use `sqlfluff` to lint and format SQL files before running `sqlc generate`.

  ```bash
  sqlfluff lint db/query.*.sql
  sqlfluff format db/query.*.sql
  ```
