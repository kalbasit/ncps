# MySQL/MariaDB Configuration

### When to Use

- Same use cases as PostgreSQL
- Existing MySQL infrastructure
- Familiarity with MySQL ecosystem

### Prerequisites

- MySQL 8.0+ or MariaDB 10.3+ server
- Database and user created
- Network connectivity from ncps to MySQL

### Setup MySQL

```
sudo mysql
```

```
CREATE DATABASE ncps CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'ncps'@'%' IDENTIFIED BY 'your-secure-password';
GRANT ALL PRIVILEGES ON ncps.* TO 'ncps'@'%';
FLUSH PRIVILEGES;
```

### Configuration

**Command-line:**

```
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

- `socket` - Path to the Unix domain socket.
- `tls=true` - Enable TLS.
- `charset=utf8mb4` - Set character encoding.

**Examples:**

```sh
# Local connection (TCP)
mysql://ncps:password@localhost:3306/ncps

# Unix Domain Socket (Standard)
mysql://ncps:password@/ncps?socket=/var/run/mysqld/mysqld.sock

# Unix Domain Socket (Specialized scheme)
mysql+unix:///var/run/mysqld/mysqld.sock/ncps

# With TLS
mysql://ncps:password@db.example.com:3306/ncps?tls=true

# With options
mysql://ncps:password@localhost:3306/ncps?charset=utf8mb4&parseTime=true
```

### Connection Pool Settings

Same as PostgreSQL:

```
ncps serve \
  --cache-database-url="mysql://..." \
  --cache-database-pool-max-open-conns=50 \
  --cache-database-pool-max-idle-conns=10
```

### Initialization

```
# Run migrations
dbmate --url="mysql://ncps:password@localhost:3306/ncps" migrate up
```

### Backup and Restore

**Backup:**

```
mysqldump -u ncps -p ncps > /backup/ncps.sql
```

**Restore:**

```
mysql -u ncps -p ncps < /backup/ncps.sql
```
