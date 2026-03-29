---
description: Roll back the last database migration
---

# Roll Back Migration

1. Determine the database engine (`sqlite`, `postgres`, or `mysql`).

1. Determine the database URL for the target engine.

1. Run the `ncps migrate down` command for the target engine. **WARNING**: This will roll back the last migration.

```bash
# SQLite
ncps migrate down --cache-database-url=sqlite:/path/to/db.sqlite

# PostgreSQL
ncps migrate down --cache-database-url=postgresql://user:pass@localhost:5432/ncps

# MySQL
ncps migrate down --cache-database-url=mysql://user:pass@localhost:3306/ncps
```

1. If you need to update schema files after rolling back, consider running `./dev-scripts/migrate-all.py` (though note it applies all `up` migrations).
