---
description: Apply all database migrations for all supported engines
---

# Apply Migrations

1. Ensure all database dependencies are running (e.g., `nix run .#deps`).

1. Run the migration script to apply migrations to all engines (PostgreSQL, MySQL, SQLite).

```bash
./dev-scripts/migrate-all.py
```

Or use `ncps migrate up` directly:

```bash
# Apply all pending migrations
ncps migrate up --cache-database-url=<database-url>
```

1. Verify that the schema files in `db/schema/` have been updated.

## Using ncps migrate up

```bash
# SQLite
ncps migrate up --cache-database-url=sqlite:/path/to/db.sqlite

# PostgreSQL
ncps migrate up --cache-database-url=postgresql://user:pass@localhost:5432/ncps

# MySQL
ncps migrate up --cache-database-url=mysql://user:pass@localhost:3306/ncps
```
