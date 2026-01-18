---
description: Apply all database migrations for all supported engines
---

# Apply Migrations

1. Ensure all database dependencies are running (e.g., `nix run .#deps`).

1. Run the migration script to apply migrations to all engines (PostgreSQL, MySQL, SQLite).

```bash
./dev-scripts/migrate-all.py
```

1. Verify that the schema files in `db/schema/` have been updated.
