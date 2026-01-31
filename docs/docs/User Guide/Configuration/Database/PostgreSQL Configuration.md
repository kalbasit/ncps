# PostgreSQL Configuration
### When to Use

*   **High availability deployments**: Multiple ncps instances
*   **Production environments**: Scalability and reliability required
*   **Large teams**: High concurrent access
*   **Cloud-native deployments**

### Prerequisites

*   PostgreSQL 12+ server
*   Database and user created
*   Network connectivity from ncps to PostgreSQL

### Setup PostgreSQL

```
# Create database and user
sudo -u postgres psql
```

```
CREATE DATABASE ncps;
CREATE USER ncps WITH PASSWORD 'your-secure-password';
GRANT ALL PRIVILEGES ON DATABASE ncps TO ncps;
\q
```

### Configuration

**Command-line:**

```
ncps serve \
  --cache-database-url="postgresql://ncps:password@localhost:5432/ncps?sslmode=require"
```

**Configuration file:**

```yaml
cache:
  database-url: postgresql://ncps:password@postgres:5432/ncps?sslmode=require
```

**Environment variable:**

```
export CACHE_DATABASE_URL="postgresql://ncps:password@localhost:5432/ncps?sslmode=require"
```

### URL Format

```
postgresql://[username]:[password]@[host]:[port]/[database]?[options]
```

**Common options:**

*   `host` - Hostname or path to the directory containing the Unix domain socket.
*   `sslmode=require` - Require TLS encryption.
*   `sslmode=disable` - Disable TLS (not recommended for production).
*   `connect_timeout=10` - Connection timeout in seconds.

**Examples:**

```sh
# Local via TCP without TLS
postgresql://ncps:password@localhost:5432/ncps?sslmode=disable

# Local via Unix Domain Socket
postgresql:///ncps?host=/var/run/postgresql

# Local via Unix Domain Socket (Specialized scheme)
postgres+unix:///var/run/postgresql/ncps

# Remote with TLS
postgresql://ncps:password@db.example.com:5432/ncps?sslmode=require

# With connection timeout
postgresql://ncps:password@localhost:5432/ncps?sslmode=require&connect_timeout=10
```

### Connection Pool Settings

**Defaults for PostgreSQL:**

*   Max open connections: 25
*   Max idle connections: 5

**Custom settings:**

```
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

> [!WARNING]
> **Advisory Locks and Connection Pools:** If you use PostgreSQL as your distributed lock backend (`--cache-lock-backend=postgres`), each active lock consumes a dedicated connection from the pool. A single request can consume up to 3 connections simultaneously.
>
> To avoid deadlocks under concurrent load, ensure `--cache-database-pool-max-open-conns` is significantly higher than your expected concurrency (at least 50-100 is recommended).

### Initialization

```
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

```
pg_dump -h localhost -U ncps ncps > /backup/ncps.sql
```

**Restore:**

```
psql -h localhost -U ncps ncps < /backup/ncps.sql
```