---
description: Create a new database migration
---
1. Read `CLAUDE.md` section "Creating Database Migrations" and "Transaction Handling" to understand the critical rules (NO manual transactions).

2. Determine the database type (sqlite, postgres, or mysql).

3. Run the creation command (example for sqlite). Do **NOT** use the `--url` flag for migration creation:
```bash
dbmate --migrations-dir db/migrations/sqlite new "your_migration_name_here"
```

4. Edit the newly created file in `db/migrations/<type>/`.

5. To run the migration, you MUST use the `--url` flag to specify the database connection:
```bash
dbmate --url "sqlite:./db.sqlite" up
```
