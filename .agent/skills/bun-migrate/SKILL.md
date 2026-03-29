---
name: bun-migrate
description: Managing database migrations with bun migrate
---

# bun migrate Skill

This skill provides comprehensive instructions for managing database migrations in this project using `bun migrate` and the `ncps migrate` CLI command.

## Migration File Structure

Migration files are stored in `db/migrations/<engine>/` directories and split into two files per migration:

```
<version>_<name>.up.sql    # forward migration
<version>_<name>.down.sql  # rollback migration
```

The version uses `YYYYMMDDHHmmss` timestamp format.

## Creating Migrations

When creating new migrations, always follow the `/migrate-new` workflow:

```bash
ncps migrate new --engine <engine> --name "migration_name"
```

Replace `<engine>` with `sqlite`, `postgres`, or `mysql`.

## Writing Migrations

### Transaction Handling

> [!IMPORTANT]
> **DO NOT** wrap your SQL in `BEGIN`/`COMMIT` blocks. bun migrate automatically wraps each migration in a transaction. Adding manual transaction blocks will cause errors and may lead to inconsistent database states.

### SQL Guidelines

1. **Idempotency**: Use `IF NOT EXISTS` or similar where possible to make migrations safer (e.g., `CREATE TABLE IF NOT EXISTS ...`).
2. **Schema Files**: **NEVER** edit files in `db/schema/` manually. They are auto-generated.
3. **Engine-Specifics**: Ensure you tailor the SQL for the specific database engine.
    - PostgreSQL: Use appropriate types (e.g., `TEXT`, `JSONB`).
    - MySQL: Use compatible types (e.g., `LONGTEXT` for JSON-like data).
    - SQLite: Be mindful of limited `ALTER TABLE` support.

## Applying Migrations

For development, use `./dev-scripts/migrate-all.py` or the `ncps migrate up` command:

```bash
# Apply all pending migrations
ncps migrate up --cache-database-url=<database-url>

# Run with a specific engine
ncps migrate up --cache-database-url=sqlite:/path/to/db.sqlite
```

## Rolling Back Migrations

Use the `ncps migrate down` command:

```bash
# Roll back the last migration
ncps migrate down --cache-database-url=<database-url>
```
