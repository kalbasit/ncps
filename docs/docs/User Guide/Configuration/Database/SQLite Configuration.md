# SQLite Configuration

### When to Use

- **Single-instance deployments**: One ncps server only
- **Simple setup**: No external database required
- **Development and testing**
- **Small to medium teams** (up to 100+ users)

**Important:** SQLite does NOT support high-availability deployments with multiple instances.

### Configuration

**Default (auto-configured):**

```
ncps serve --cache-hostname=cache.example.com
# Uses embedded SQLite automatically
```

**Explicit path:**

```
ncps serve --cache-database-url=sqlite:/var/lib/ncps/db/db.sqlite
```

**Configuration file:**

```yaml
cache:
  database-url: sqlite:/var/lib/ncps/db/db.sqlite
```

### Database File Location

The SQLite database is stored as a single file:

```
/var/lib/ncps/db/db.sqlite
```

### Initialization

```
# Run migrations to create tables
dbmate --url=sqlite:/var/lib/ncps/db/db.sqlite migrate up
```

### Connection Pool Settings

SQLite enforces a maximum of 1 open connection:

```
# This is automatically set for SQLite
--cache-database-pool-max-open-conns=1
```

**Note:** Setting this value higher than 1 for SQLite will cause errors.

### Performance Characteristics

**Pros:**

- Zero configuration
- Fast for single-instance
- No network overhead
- Automatic backups (file copy)

**Cons:**

- Single writer at a time
- Not suitable for HA
- Limited scalability

### Backup and Restore

**Backup:**

```
# Stop ncps (optional but recommended)
systemctl stop ncps

# Copy database file
cp /var/lib/ncps/db/db.sqlite /backup/db.sqlite.$(date +%Y%m%d)

# Restart ncps
systemctl start ncps
```

**Restore:**

```
systemctl stop ncps
cp /backup/db.sqlite.20240101 /var/lib/ncps/db/db.sqlite
systemctl start ncps
```
