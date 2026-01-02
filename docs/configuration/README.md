[Home](../../README.md) > [Documentation](../README.md) > Configuration

# Configuration Guide

Learn how to configure ncps for your specific needs.

## Configuration Files

- **[Configuration Reference](reference.md)** - Complete reference of all configuration options
- **[Storage Configuration](storage.md)** - Configure local or S3 storage backends
- **[Database Configuration](database.md)** - Configure SQLite, PostgreSQL, or MySQL
- **[Observability Configuration](observability.md)** - Set up metrics, logging, and tracing

## Quick Links

### By Topic

**Getting Started**
- [All configuration options](reference.md)
- [Example configuration file](../../config.example.yaml)

**Storage**
- [Local filesystem storage](storage.md#local-storage)
- [S3-compatible storage](storage.md#s3-storage)
- [Storage comparison](storage.md#comparison)

**Database**
- [SQLite configuration](database.md#sqlite)
- [PostgreSQL configuration](database.md#postgresql)
- [MySQL/MariaDB configuration](database.md#mysql)
- [Database comparison](database.md#comparison)

**Observability**
- [Prometheus metrics](observability.md#prometheus)
- [OpenTelemetry setup](observability.md#opentelemetry)
- [Logging configuration](observability.md#logging)

## Configuration Methods

ncps supports multiple ways to configure the service:

### 1. Command-line Flags

```bash
ncps serve \
  --cache-hostname=cache.example.com \
  --cache-storage-local=/var/lib/ncps \
  --cache-database-url=sqlite:/var/lib/ncps/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

### 2. Environment Variables

```bash
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
```bash
ncps serve --config=config.yaml
```

**Supported formats:**
- YAML (`.yaml`, `.yml`)
- TOML (`.toml`)
- JSON (`.json`)

See [config.example.yaml](../../config.example.yaml) for a complete example.

### 4. Combination

Configuration methods can be combined. Priority (highest to lowest):
1. Command-line flags
2. Environment variables
3. Configuration file
4. Defaults

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

```bash
# Dry-run to check configuration
ncps serve --config=config.yaml --help

# Check for errors in logs
ncps serve --config=config.yaml --log-level=debug
```

## Next Steps

1. **[Configuration Reference](reference.md)** - See all available options
2. **[Storage Configuration](storage.md)** - Choose and configure storage backend
3. **[Database Configuration](database.md)** - Choose and configure database
4. **[Observability Configuration](observability.md)** - Set up monitoring

## Related Documentation

- [config.example.yaml](../../config.example.yaml) - Complete configuration example
- [Installation Guides](../installation/) - Installation-specific configuration
- [Deployment Guides](../deployment/) - Deployment-specific configuration
