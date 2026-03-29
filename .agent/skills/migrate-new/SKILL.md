---
description: Create a new database migration for all supported engines
---

# Create New Migration

1. Determine the migration name (e.g., `add_users_table`).

1. Run the creation command for each supported engine using `ncps migrate new`:

```bash
ncps migrate new --engine sqlite --name "your_migration_name_here"
ncps migrate new --engine postgres --name "your_migration_name_here"
ncps migrate new --engine mysql --name "your_migration_name_here"
```

1. Edit the newly created `.up.sql` and `.down.sql` files in `db/migrations/<engine>/`.

1. Follow the **bun-migrate skill** (`.agent/skills/dbmate/SKILL.md`) for rules on writing migrations (transaction handling, idempotent SQL, etc.).

## Migration File Naming

Migration files use the format: `<version>_<name>.up.sql` and `<version>_<name>.down.sql`

The version is automatically generated as a timestamp (YYYYMMDDHHmmss format).
