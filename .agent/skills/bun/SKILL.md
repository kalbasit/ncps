---
name: bun
description: Working with Bun query builder for database operations
---

# Bun Skill

This skill provides instructions for working with `bun` as the database query builder in the NCPS repository. NCPS supports multiple database engines (SQLite, PostgreSQL, MySQL), and `bun` provides a consistent ORM-style interface across all engines.

## Overview

NCPS uses `bun` (from `github.com/uptrace/bun`) for type-safe database operations. Unlike the previous `sqlc` approach which generated code from SQL files, `bun` uses a query builder pattern that is written directly in Go code.

## Key Packages

- `github.com/uptrace/bun` - Main bun package
- `github.com/uptrace/bun/dialect/sqlitedialect` - SQLite dialect
- `github.com/uptrace/bun/dialect/pgdialect` - PostgreSQL dialect
- `github.com/uptrace/bun/dialect/mysqldialect` - MySQL dialect

## Database Package Structure

The database package (`pkg/database/`) provides:

- `database.Open()` - Opens a database connection based on URL scheme
- `database.Querier` - Common interface for all database operations
- Engine-specific wrappers that implement the Querier interface

## Writing Database Queries

When adding or modifying database queries:

### Pattern 1: Query Builder

Use bun's query builder for type-safe SQL construction:

```go
query := db.NewSelect().Model(&items).Where("id = ?", id)
err := query.Scan(ctx, &items)
```

### Pattern 2: Raw SQL with bun

For complex queries, use bun's SQL builder:

```go
err := db.NewRaw("SELECT * FROM items WHERE id = ?", id).Scan(ctx, &item)
```

## Best Practices

1. **Consistency**: Ensure that equivalent operations work across all supported engines unless the feature is engine-specific.
2. **Linting**: Use `sqlfluff` to lint and format SQL strings within Go code.
3. **Transactions**: Use bun's transaction support for atomic operations:

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer tx.Rollback()

// ... operations ...

return tx.Commit()
```

4. **Connection Pooling**: Configure connection pools appropriately for each engine.

## Migrations

Migrations are handled by the `ncps migrate` command using `bun migrate`. See the `/migrate-new`, `/migrate-up`, and `/migrate-down` skills for migration workflows.
