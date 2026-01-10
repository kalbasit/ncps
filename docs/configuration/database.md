[Home](../../README.md) > [Documentation](../README.md) > [Configuration](README.md) > Database

# Database Configuration

Configure ncps database backends: SQLite, PostgreSQL, or MySQL/MariaDB.

## Overview

ncps supports three database backends for storing metadata (NarInfo, cache statistics, etc.):

- **SQLite**: Embedded database, no external dependencies
- **PostgreSQL**: Production-ready, concurrent access support
- **MySQL/MariaDB**: Production-ready, concurrent access support

## Quick Comparison

| Feature | SQLite | PostgreSQL | MySQL/MariaDB |
|---------|--------|------------|---------------|
| **Setup Complexity** | None (embedded) | Moderate | Moderate |
| **External Service** | ❌ No | ✅ Yes | ✅ Yes |
| **Concurrent Writes** | ❌ Limited (1 connection) | ✅ Excellent | ✅ Excellent |
| **HA Support** | ❌ Not supported | ✅ Supported | ✅ Supported |
| **Performance** | Good (embedded) | Excellent | Excellent |
| **Best For** | Single-instance | HA, Production | HA, Production |

## SQLite Configuration

### When to Use

- **Single-instance deployments**: One ncps server only
- **Simple setup**: No external database required
- **Development and testing**
- **Small to medium teams** (up to 100+ users)

**Important:** SQLite does NOT support high-availability deployments with multiple instances.

### Configuration

**Default (auto-configured):**

```bash
ncps serve --cache-hostname=cache.example.com
# Uses embedded SQLite automatically
```

**Explicit path:**

```bash
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

```bash
# Run migrations to create tables
dbmate --url=sqlite:/var/lib/ncps/db/db.sqlite migrate up
```

### Connection Pool Settings

SQLite enforces a maximum of 1 open connection:

```bash
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

```bash
# Stop ncps (optional but recommended)
systemctl stop ncps

# Copy database file
cp /var/lib/ncps/db/db.sqlite /backup/db.sqlite.$(date +%Y%m%d)

# Restart ncps
systemctl start ncps
```

**Restore:**

```bash
systemctl stop ncps
cp /backup/db.sqlite.20240101 /var/lib/ncps/db/db.sqlite
systemctl start ncps
```

## PostgreSQL Configuration

### When to Use

- **High availability deployments**: Multiple ncps instances
- **Production environments**: Scalability and reliability required
- **Large teams**: High concurrent access
- **Cloud-native deployments**

### Prerequisites

- PostgreSQL 12+ server
- Database and user created
- Network connectivity from ncps to PostgreSQL

### Setup PostgreSQL

```bash
# Create database and user
sudo -u postgres psql
```

```sql
CREATE DATABASE ncps;
CREATE USER ncps WITH PASSWORD 'your-secure-password';
GRANT ALL PRIVILEGES ON DATABASE ncps TO ncps;
\q
```

### Configuration

**Command-line:**

```bash
ncps serve \
  --cache-database-url="postgresql://ncps:password@localhost:5432/ncps?sslmode=require"
```

**Configuration file:**

```yaml
cache:
  database-url: postgresql://ncps:password@postgres:5432/ncps?sslmode=require
```

**Environment variable:**

```bash
export CACHE_DATABASE_URL="postgresql://ncps:password@localhost:5432/ncps?sslmode=require"
```

### URL Format

```
postgresql://[username]:[password]@[host]:[port]/[database]?[options]
```

**Common options:**

- `sslmode=require` - Require TLS encryption
- `sslmode=disable` - Disable TLS (not recommended for production)
- `connect_timeout=10` - Connection timeout in seconds

**Examples:**

```
# Local without TLS
postgresql://ncps:password@localhost:5432/ncps?sslmode=disable

# Remote with TLS
postgresql://ncps:password@db.example.com:5432/ncps?sslmode=require

# With connection timeout
postgresql://ncps:password@localhost:5432/ncps?sslmode=require&connect_timeout=10
```

### Connection Pool Settings

**Defaults for PostgreSQL:**

- Max open connections: 25
- Max idle connections: 5

**Custom settings:**

```bash
ncps serve \
  --cache-database-url="postgresql://..." \
  --cache-database-pool-max-open-conns=50 \
  --cache-database-pool-max-idle-conns=10
```

**Configuration file:**

```yaml
cache:
  database-url: postgresql://ncps:password@postgres:5432/ncps
  database:
    pool:
      max-open-conns: 50
      max-idle-conns: 10
```

### Initialization

```bash
# Run migrations
dbmate --url="postgresql://ncps:password@localhost:5432/ncps?sslmode=disable" migrate up
```

### Performance Tuning

**PostgreSQL server configuration** (`postgresql.conf`):

```
max_connections = 100
shared_buffers = 256MB
effective_cache_size = 1GB
work_mem = 4MB
maintenance_work_mem = 64MB
```

### Backup and Restore

**Backup:**

```bash
pg_dump -h localhost -U ncps ncps > /backup/ncps.sql
```

**Restore:**

```bash
psql -h localhost -U ncps ncps < /backup/ncps.sql
```

## MySQL/MariaDB Configuration

### When to Use

- Same use cases as PostgreSQL
- Existing MySQL infrastructure
- Familiarity with MySQL ecosystem

### Prerequisites

- MySQL 8.0+ or MariaDB 10.3+ server
- Database and user created
- Network connectivity from ncps to MySQL

### Setup MySQL

```bash
sudo mysql
```

```sql
CREATE DATABASE ncps CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'ncps'@'%' IDENTIFIED BY 'your-secure-password';
GRANT ALL PRIVILEGES ON ncps.* TO 'ncps'@'%';
FLUSH PRIVILEGES;
```

### Configuration

**Command-line:**

```bash
ncps serve \
  --cache-database-url="mysql://ncps:password@localhost:3306/ncps"
```

**Configuration file:**

```yaml
cache:
  database-url: mysql://ncps:password@mysql:3306/ncps
```

### URL Format

```
mysql://[username]:[password]@[host]:[port]/[database]?[options]
```

**Common options:**

- `tls=true` - Enable TLS
- `charset=utf8mb4` - Set character encoding

**Examples:**

```
# Local connection
mysql://ncps:password@localhost:3306/ncps

# With TLS
mysql://ncps:password@db.example.com:3306/ncps?tls=true

# With options
mysql://ncps:password@localhost:3306/ncps?charset=utf8mb4&parseTime=true
```

### Connection Pool Settings

Same as PostgreSQL:

```bash
ncps serve \
  --cache-database-url="mysql://..." \
  --cache-database-pool-max-open-conns=50 \
  --cache-database-pool-max-idle-conns=10
```

### Initialization

```bash
# Run migrations
dbmate --url="mysql://ncps:password@localhost:3306/ncps" migrate up
```

### Backup and Restore

**Backup:**

```bash
mysqldump -u ncps -p ncps > /backup/ncps.sql
```

**Restore:**

```bash
mysql -u ncps -p ncps < /backup/ncps.sql
```

## Migration Between Databases

### From SQLite to PostgreSQL

```bash
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

```bash
pgloader sqlite:///var/lib/ncps/db/db.sqlite \
  postgresql://ncps:password@localhost:5432/ncps
```

### From SQLite to MySQL

Similar process using tools like:

- `sqlite3mysql` utility
- Manual export and conversion
- Custom migration scripts

## Troubleshooting

### SQLite Issues

**Database Locked:**

- Only one writer at a time
- Ensure no other processes are accessing the database
- Check for stale lock files

**Corruption:**

```bash
# Check integrity
sqlite3 /var/lib/ncps/db/db.sqlite "PRAGMA integrity_check;"

# Recover if possible
sqlite3 /var/lib/ncps/db/db.sqlite ".recover" | sqlite3 recovered.db
```

### PostgreSQL Issues

**Connection Refused:**

- Check PostgreSQL is running: `systemctl status postgresql`
- Verify `pg_hba.conf` allows connections from ncps host
- Check firewall rules

**Authentication Failed:**

- Verify username and password
- Check `pg_hba.conf` authentication method
- Ensure user has correct privileges

**Too Many Connections:**

- Reduce pool size in ncps
- Increase `max_connections` in PostgreSQL
- Check for connection leaks

### MySQL Issues

**Connection Refused:**

- Check MySQL is running: `systemctl status mysql`
- Verify `bind-address` in my.cnf
- Check firewall rules

**Access Denied:**

- Verify username, password, and host in GRANT
- Check user privileges: `SHOW GRANTS FOR 'ncps'@'%';`
- Flush privileges after changes

See the [Troubleshooting Guide](../operations/troubleshooting.md) for more help.

## Next Steps

1. **[Storage Configuration](storage.md)** - Configure storage backend
1. **[Configuration Reference](reference.md)** - All database options
1. **[High Availability](../deployment/high-availability.md)** - PostgreSQL/MySQL for HA
1. **[Operations Guide](../operations/backup-restore.md)** - Backup strategies

## Related Documentation

- [Configuration Reference](reference.md) - All configuration options
- [High Availability Guide](../deployment/high-availability.md) - HA database setup
- [Backup and Restore Guide](../operations/backup-restore.md) - Backup strategies
