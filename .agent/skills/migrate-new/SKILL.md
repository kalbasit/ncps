---
description: Create a new database migration for all supported engines
---

# Create New Migration

1. Determine the migration name (e.g., `add_users_table`).

1. Run the creation command for each supported engine. DO **NOT** use the `--url` flag:

```bash
dbmate --migrations-dir db/migrations/sqlite new "your_migration_name_here"
dbmate --migrations-dir db/migrations/postgres new "your_migration_name_here"
dbmate --migrations-dir db/migrations/mysql new "your_migration_name_here"
```

1. Edit the newly created files in `db/migrations/<engine>/`.

1. Follow the **dbmate skill** (`.agent/skills/dbmate/SKILL.md`) for rules on writing migrations (transaction handling, idempotent SQL, etc.).
