# Configuration

## Configuration Guide

Learn how to configure ncps for your specific needs.

## Configuration Files

- <a class="reference-link" href="Configuration/Reference.md">Reference</a> - Complete reference of all configuration options
- <a class="reference-link" href="Configuration/Storage.md">Storage</a> - Configure local or S3 storage backends
- <a class="reference-link" href="Configuration/Database.md">Database</a> - Configure SQLite, PostgreSQL, or MySQL
- <a class="reference-link" href="Configuration/Analytics.md">Analytics</a> - Configure anonymous usage statistics reporting
- <a class="reference-link" href="Configuration/Observability.md">Observability</a> - Set up metrics, logging, and tracing

## Quick Links

### By Topic

**Getting Started**

- [All configuration options](Configuration/Reference.md)
- [Example Configuration](https://github.com/kalbasit/ncps/blob/main/config.example.yaml)

**Storage**

- <a class="reference-link" href="Configuration/Storage/Local%20Filesystem%20Storage.md">Local Filesystem Storage</a>
- <a class="reference-link" href="Configuration/Storage/S3-Compatible%20Storage.md">S3-Compatible Storage</a>
- <a class="reference-link" href="Configuration/Storage/Storage%20Comparison.md">Storage Comparison</a>

**Database**

- <a class="reference-link" href="Configuration/Database/SQLite%20Configuration.md">SQLite Configuration</a>
- <a class="reference-link" href="Configuration/Database/PostgreSQL%20Configuration.md">PostgreSQL Configuration</a>
- <a class="reference-link" href="Configuration/Database/MySQLMariaDB%20Configuration.md">MySQL/MariaDB Configuration</a>
- [Database Migrations](Configuration/Database/Migration%20Between%20Databases.md)

**Observability**

- [Prometheus Metrics](Configuration/Observability.md)
- [Logging Setup](Configuration/Observability.md)
- [Tracing Setup](Configuration/Observability.md)

**Privacy & Analytics**

- [Analytics Overview](Configuration/Analytics.md)
- [Data Collection](Configuration/Analytics.md)
- [Opt-out Guide](Configuration/Analytics.md)

## Configuration Methods

ncps supports multiple ways to configure the service:

### 1. Command-line Flags

```sh
ncps serve \
  --cache-hostname=cache.example.com \
  --cache-storage-local=/var/lib/ncps \
  --cache-database-url=sqlite:/var/lib/ncps/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

### 2. Environment Variables

```sh
export CACHE_HOSTNAME=cache.example.com
export CACHE_STORAGE_LOCAL=/var/lib/ncps
export CACHE_DATABASE_URL=sqlite:/var/lib/ncps/db/db.sqlite
export CACHE_UPSTREAM_URLS=https://cache.nixos.org
export CACHE_UPSTREAM_PUBLIC_KEYS=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=

ncps serve
```

### 3. Configuration File

Create `config.yaml`:

```yaml
cache:
  hostname: cache.example.com
  storage:
    local: /var/lib/ncps
  database-url: sqlite:/var/lib/ncps/db/db.sqlite
  upstream:
    urls:
      - https://cache.nixos.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

Then:

```sh
ncps serve --config=config.yaml
```

**Supported formats:**

- YAML (`.yaml`, `.yml`)
- TOML (`.toml`)
- JSON (`.json`)

See [Example Configuration](https://github.com/kalbasit/ncps/blob/main/config.example.yaml) for a complete example.

### 4. Combination

Configuration methods can be combined. Priority (highest to lowest):

1. Command-line flags
1. Environment variables
1. Configuration file
1. Defaults

## Common Configuration Scenarios

### Single-Instance with Local Storage

```yaml
cache:
  hostname: cache.example.com
  storage:
    local: /var/lib/ncps
  database-url: sqlite:/var/lib/ncps/db/db.sqlite
  max-size: 50G
  lru:
    schedule: "0 2 * * *"  # Daily at 2 AM
  upstream:
    urls:
      - https://cache.nixos.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

### Production with S3 and PostgreSQL

```yaml
cache:
  hostname: cache.example.com
  storage:
    s3:
      bucket: ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      access-key-id: ${S3_ACCESS_KEY}
      secret-access-key: ${S3_SECRET_KEY}
      force-path-style: false  # Set to true for MinIO
  database-url: postgresql://ncps:password@postgres:5432/ncps?sslmode=require
  max-size: 200G
  upstream:
    urls:
      - https://cache.nixos.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
prometheus:
  enabled: true
```

### High Availability

```yaml
cache:
  hostname: cache.example.com
  storage:
    s3:
      bucket: ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      force-path-style: false  # Set to true for MinIO
  database-url: postgresql://ncps:password@postgres:5432/ncps?sslmode=require
  redis:
    addrs:
      - redis-node1:6379
      - redis-node2:6379
      - redis-node3:6379
    password: ${REDIS_PASSWORD}
  lock:
    download-lock-ttl: 5m
    lru-lock-ttl: 30m
  upstream:
    urls:
      - https://cache.nixos.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

## Required vs Optional Configuration

### Required Options

These must be set for ncps to start:

- `cache.hostname` - Hostname for key generation
- Storage backend (choose one):
  - `cache.storage.local` OR
  - `cache.storage.s3.*` (bucket, endpoint, credentials)
- `cache.upstream.urls` - At least one upstream cache
- `cache.upstream.public-keys` - Corresponding public keys

### Optional But Recommended

- `cache.max-size` - Prevent unbounded cache growth
- `cache.lru.schedule` - Automatic cleanup
- `prometheus.enabled` - Metrics for monitoring
- `cache.database-url` - For shared database (default: embedded SQLite)

## Validation

Validate your configuration before deploying:

```sh
# Dry-run to check configuration
ncps serve --config=config.yaml --help

# Check for errors in logs
ncps serve --config=config.yaml --log-level=debug
```

## Next Steps

1. <a class="reference-link" href="Configuration/Reference.md">Reference</a> - See all available options
1. <a class="reference-link" href="Configuration/Storage.md">Storage</a> - Choose and configure storage backend
1. <a class="reference-link" href="Configuration/Database.md">Database</a> - Choose and configure database
1. <a class="reference-link" href="Configuration/Observability.md">Observability</a> - Set up monitoring

## Related Documentation

- [Example configuration file](https://github.com/kalbasit/ncps/blob/main/config.example.yaml) - Complete configuration example
- [Installation Guides](Installation.md) - Installation-specific configuration
- [Observability Configuration](Configuration/Observability.md) - Deployment-specific configuration
