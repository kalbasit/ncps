---
name: dbmate
description: Managing database migrations with dbmate
---

# dbmate Skill

This skill provides comprehensive instructions for managing database migrations in this project using `dbmate`.

## Creating Migrations

When creating new migrations, always follow the `/migrate-new` workflow. The primary tool for creating a migration is:

```bash
dbmate --migrations-dir db/migrations/<engine> new "migration_name"
```

Replace `<engine>` with `sqlite`, `postgres`, or `mysql`.

## Writing Migrations

Migrations are stored in `db/migrations/<engine>/` as `.sql` files. They follow a specific format:

### Structure

Each migration file MUST have two sections:

```sql
-- migrate:up
-- SQL statements to apply the change

-- migrate:down
-- SQL statements to reverse the change
```

### Transaction Handling

> [!IMPORTANT]
> **DO NOT** wrap your SQL in `BEGIN`/`COMMIT` blocks. `dbmate` automatically wraps each migration in a transaction. Adding manual transaction blocks will cause errors and may lead to inconsistent database states.

### SQL Guidelines

1. **Idempotency**: Use `IF NOT EXISTS` or similar where possible to make migrations safer (e.g., `CREATE TABLE IF NOT EXISTS ...`).
2. **Schema Files**: **NEVER** edit files in `db/schema/` manually. They are auto-generated.
3. **Engine-Specifics**: Ensure you tailor the SQL for the specific database engine.
    - PostgreSQL: Use appropriate types (e.g., `TEXT`, `JSONB`).
    - MySQL: Use compatible types (e.g., `LONGTEXT` for JSON-like data).
    - SQLite: Be mindful of limited `ALTER TABLE` support.

## Applying Migrations

For development, use `./dev-scripts/migrate-all.py`. This script:

1. Applies migrations for all engines (PostgreSQL, MySQL, SQLite).
2. Updates the schema files in `db/schema/`.
