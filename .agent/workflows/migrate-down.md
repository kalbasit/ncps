---
description: Roll back the last database migration
---

# Roll Back Migration

1. Determine the database engine (`sqlite`, `postgres`, or `mysql`).

1. Determine the database URL for the target engine.

1. Run the `dbmate down` command for the target engine. **WARNING**: This will roll back the last migration.

```bash
dbmate --url "your_db_url_here" down
```

1. If you need to update schema files after rolling back, consider running `./dev-scripts/migrate-all.py` (though note it applies all `up` migrations).
