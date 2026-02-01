# Migration Between Databases

### From SQLite to PostgreSQL

```
# 1. Export SQLite data
sqlite3 /var/lib/ncps/db/db.sqlite .dump > dump.sql

# 2. Convert SQL (SQLite → PostgreSQL syntax)
# Use a tool like pgloader or manually edit

# 3. Import to PostgreSQL
psql -U ncps -d ncps -f converted.sql

# 4. Update ncps configuration
# 5. Restart ncps
```

**Using pgloader (recommended):**

```
pgloader sqlite:///var/lib/ncps/db/db.sqlite \
  postgresql://ncps:password@localhost:5432/ncps
```

### From SQLite to MySQL

Similar process using tools like:

- `sqlite3mysql` utility
- Manual export and conversion
- Custom migration scripts
