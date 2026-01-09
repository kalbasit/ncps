[Home](../../README.md) > [Documentation](../README.md) > [Configuration](README.md) > Reference

# Configuration Reference

Complete reference for all ncps configuration options.

## Table of Contents

- [Global Options](#global-options)
- [Server Configuration](#server-configuration)
- [Essential Options](#essential-options)
- [Storage Options](#storage-options)
- [Database & Performance](#database--performance)
- [Security & Signing](#security--signing)
- [Upstream Connection Timeouts](#upstream-connection-timeouts)
- [Redis Configuration (HA)](#redis-configuration-ha)
- [Lock Configuration (HA)](#lock-configuration-ha)
- [Analytics Reporting](#analytics-reporting)
- [Observability](#observability)

## Global Options

Options that apply to the entire ncps process.

| Option | Description | Environment Variable | Default |
| ------------------------------- | ---------------------------------------------------------------------- | ----------------------------- | ----------------------------------- |
| `--config` | Path to configuration file (json, toml, yaml) | `NCPS_CONFIG_FILE` | `$XDG_CONFIG_HOME/ncps/config.yaml` |
| `--log-level` | Log level: debug, info, warn, error | `LOG_LEVEL` | `info` |
| `--otel-enabled` | Enable OpenTelemetry (logs, metrics, tracing) | `OTEL_ENABLED` | `false` |
| `--otel-grpc-url` | OpenTelemetry gRPC collector URL (omit for stdout) | `OTEL_GRPC_URL` | - |
| `--prometheus-enabled` | Enable Prometheus metrics endpoint at /metrics | `PROMETHEUS_ENABLED` | `false` |

**Example:**

```bash
ncps serve \
  --config=/etc/ncps/config.yaml \
  --log-level=debug \
  --prometheus-enabled=true
```

## Server Configuration

Network and server behavior options.

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--server-addr` | Listen address and port | `SERVER_ADDR` | `:8501` |

**Example:**

```bash
ncps serve --server-addr=0.0.0.0:8501
```

## Essential Options

Required configuration for ncps to function.

| Option | Description | Environment Variable | Required |
|--------|-------------|---------------------|----------|
| `--cache-hostname` | Cache hostname for key generation | `CACHE_HOSTNAME` | ✅ Yes |
| `--cache-storage-local` | Local storage directory (use this OR S3) | `CACHE_STORAGE_LOCAL` | ✅ One storage backend required |
| `--cache-upstream-url` | Upstream cache URL (repeatable for multiple upstreams) | `CACHE_UPSTREAM_URLS` | ✅ Yes |
| `--cache-upstream-public-key` | Upstream public key (repeatable, matches urls) | `CACHE_UPSTREAM_PUBLIC_KEYS` | ✅ Yes |

**Note:** Either `--cache-storage-local` OR all S3 storage flags must be provided, but not both.

**Example:**

```bash
ncps serve \
  --cache-hostname=cache.example.com \
  --cache-storage-local=/var/lib/ncps \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-url=https://nix-community.cachix.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY= \
  --cache-upstream-public-key=nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=
```

## Storage Options

### Local Filesystem Storage

| Option | Description | Environment Variable | Required |
|--------|-------------|---------------------|----------|
| `--cache-storage-local` | Local storage directory path | `CACHE_STORAGE_LOCAL` | ✅ (if not using S3) |

**Example:**

```bash
ncps serve --cache-storage-local=/var/lib/ncps
```

### S3-Compatible Storage

Use these options for S3-compatible storage (AWS S3, MinIO, etc.).

| Option | Description | Environment Variable | Required for S3 | Default |
|--------|-------------|---------------------|-----------------|---------|
| `--cache-storage-s3-bucket` | S3 bucket name | `CACHE_STORAGE_S3_BUCKET` | ✅ | - |
| `--cache-storage-s3-endpoint` | S3 endpoint URL with scheme (e.g., https://s3.amazonaws.com or http://minio:9000) | `CACHE_STORAGE_S3_ENDPOINT` | ✅ | - |
| `--cache-storage-s3-access-key-id` | S3 access key ID | `CACHE_STORAGE_S3_ACCESS_KEY_ID` | ✅ | - |
| `--cache-storage-s3-secret-access-key` | S3 secret access key | `CACHE_STORAGE_S3_SECRET_ACCESS_KEY` | ✅ | - |
| `--cache-storage-s3-region` | S3 region (optional for some providers) | `CACHE_STORAGE_S3_REGION` | - | - |
| `--cache-storage-s3-force-path-style` | Use path-style URLs (required for MinIO) | `CACHE_STORAGE_S3_FORCE_PATH_STYLE` | - | `false` |
| `--cache-storage-s3-use-ssl` | **DEPRECATED:** Specify scheme in endpoint instead | `CACHE_STORAGE_S3_USE_SSL` | - | - |

**Note:** The endpoint must include the scheme (`https://` or `http://`). The `--cache-storage-s3-use-ssl` flag is deprecated in favor of specifying the scheme directly in the endpoint URL.

**AWS S3 Example:**

```bash
ncps serve \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=https://s3.amazonaws.com \
  --cache-storage-s3-region=us-east-1 \
  --cache-storage-s3-access-key-id=AKIAIOSFODNN7EXAMPLE \
  --cache-storage-s3-secret-access-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

**MinIO Example:**

```bash
ncps serve \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=http://minio.example.com:9000 \
  --cache-storage-s3-access-key-id=minioadmin \
  --cache-storage-s3-secret-access-key=minioadmin \
  --cache-storage-s3-force-path-style=true
```

**Important:** MinIO requires `--cache-storage-s3-force-path-style=true` for proper S3 compatibility.

See [Storage Configuration](storage.md) for details.

## Database & Performance

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-database-url` | Database URL (sqlite://, postgresql://, mysql://) | `CACHE_DATABASE_URL` | Embedded SQLite |
| `--cache-database-pool-max-open-conns` | Maximum open database connections | `CACHE_DATABASE_POOL_MAX_OPEN_CONNS` | 25 (PG/MySQL), 1 (SQLite) |
| `--cache-database-pool-max-idle-conns` | Maximum idle database connections | `CACHE_DATABASE_POOL_MAX_IDLE_CONNS` | 5 (PG/MySQL), unset (SQLite) |
| `--cache-max-size` | Maximum cache size (5K, 10G, etc.) | `CACHE_MAX_SIZE` | unlimited |
| `--cache-lru-schedule` | LRU cleanup cron schedule | `CACHE_LRU_SCHEDULE` | - |
| `--cache-temp-path` | Temporary download directory | `CACHE_TEMP_PATH` | system temp |

**Database URL Formats:**

- SQLite: `sqlite:/var/lib/ncps/db/db.sqlite`
- PostgreSQL: `postgresql://user:pass@host:5432/database?sslmode=require`
- MySQL: `mysql://user:pass@host:3306/database`

**Example:**

```bash
ncps serve \
  --cache-database-url=postgresql://ncps:password@localhost:5432/ncps?sslmode=require \
  --cache-max-size=100G \
  --cache-lru-schedule="0 2 * * *"  # Daily at 2 AM
```

See [Database Configuration](database.md) for details.

## Security & Signing

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-sign-narinfo` | Sign NarInfo files with private key | `CACHE_SIGN_NARINFO` | `true` |
| `--cache-secret-key-path` | Path to signing private key | `CACHE_SECRET_KEY_PATH` | auto-generated |
| `--cache-allow-put-verb` | Allow PUT uploads to cache | `CACHE_ALLOW_PUT_VERB` | `false` |
| `--cache-allow-delete-verb` | Allow DELETE operations on cache | `CACHE_ALLOW_DELETE_VERB` | `false` |
| `--netrc-file` | Path to netrc file for upstream auth | `NETRC_FILE` | `~/.netrc` |

**Example:**

```bash
ncps serve \
  --cache-secret-key-path=/etc/ncps/secret-key \
  --cache-allow-put-verb=true \
  --netrc-file=/etc/ncps/netrc
```

## Upstream Connection Timeouts

Configure timeout values for upstream cache connections. Increase these if experiencing timeout errors with slow or remote upstreams.

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-upstream-dialer-timeout` | TCP connection establishment timeout | `CACHE_UPSTREAM_DIALER_TIMEOUT` | `3s` |
| `--cache-upstream-response-header-timeout` | Response header waiting timeout | `CACHE_UPSTREAM_RESPONSE_HEADER_TIMEOUT` | `3s` |

**Common timeout values:**

- `3s` - Default, works for most local/fast upstreams
- `10s` - Recommended for slow networks or distant upstreams
- `30s` - For very slow connections (satellite, slow VPN)

**Example:**

```bash
ncps serve \
  --cache-upstream-dialer-timeout=10s \
  --cache-upstream-response-header-timeout=10s
```

## Redis Configuration (HA)

Redis configuration for distributed locking in high-availability deployments.

### Connection Settings

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-redis-addrs` | Redis addresses (comma-separated for cluster) | `CACHE_REDIS_ADDRS` | (none - local mode) |
| `--cache-redis-username` | Redis ACL username | `CACHE_REDIS_USERNAME` | "" |
| `--cache-redis-password` | Redis password | `CACHE_REDIS_PASSWORD` | "" |
| `--cache-redis-db` | Redis database number (0-15) | `CACHE_REDIS_DB` | 0 |
| `--cache-redis-use-tls` | Enable TLS for Redis connections | `CACHE_REDIS_USE_TLS` | false |
| `--cache-redis-pool-size` | Connection pool size | `CACHE_REDIS_POOL_SIZE` | 10 |

**Note:** If `--cache-redis-addrs` is not provided, ncps runs in single-instance mode using local locks.

**Single Redis Instance Example:**

```bash
ncps serve --cache-redis-addrs=redis:6379
```

**Redis Cluster Example:**

```bash
ncps serve \
  --cache-redis-addrs=node1:6379,node2:6379,node3:6379 \
  --cache-redis-password=secret
```

**TLS with Authentication:**

```bash
ncps serve \
  --cache-redis-use-tls=true \
  --cache-redis-username=ncps \
  --cache-redis-password=secret \
  --cache-redis-addrs=redis.example.com:6380
```

See [High Availability Guide](../deployment/high-availability.md) and [Distributed Locking Guide](../deployment/distributed-locking.md) for more details.

## Lock Configuration (HA)

Lock timing and retry configuration for distributed locking.

### Backend Lock Settings

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-lock-redis-key-prefix` | Key prefix for all Redis locks | `CACHE_LOCK_REDIS_KEY_PREFIX` | `"ncps:lock:"` |
| `--cache-lock-postgres-key-prefix` | Key prefix for all PostgreSQL locks | `CACHE_LOCK_POSTGRES_KEY_PREFIX` | `"ncps:lock:"` |

### Lock Timeouts

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-lock-download-ttl` | Download lock timeout (per-hash) | `CACHE_LOCK_DOWNLOAD_TTL` | `5m` |
| `--cache-lock-lru-ttl` | LRU lock timeout (global) | `CACHE_LOCK_LRU_TTL` | `30m` |

### Retry Configuration

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--cache-lock-retry-max-attempts` | Maximum lock retry attempts | `CACHE_LOCK_RETRY_MAX_ATTEMPTS` | 3 |
| `--cache-lock-retry-initial-delay` | Initial retry delay | `CACHE_LOCK_RETRY_INITIAL_DELAY` | `100ms` |
| `--cache-lock-retry-max-delay` | Maximum retry delay (backoff cap) | `CACHE_LOCK_RETRY_MAX_DELAY` | `2s` |
| `--cache-lock-retry-jitter` | Enable jitter in retry delays | `CACHE_LOCK_RETRY_JITTER` | `true` |
| `--cache-lock-allow-degraded-mode` | Fallback to local locks if Redis unavailable | `CACHE_LOCK_ALLOW_DEGRADED_MODE` | `false` |

**Example:**

```bash
ncps serve \
  --cache-lock-download-ttl=10m \
  --cache-lock-lru-ttl=1h \
  --cache-lock-retry-max-attempts=5 \
  --cache-lock-retry-max-delay=5s
```

See [Distributed Locking Guide](../deployment/distributed-locking.md) for tuning guidance.

## Analytics Reporting

Configure anonymous usage statistics reporting to help improve ncps.

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--analytics-reporting-enabled` | Enable anonymous usage statistics reporting | `ANALYTICS_REPORTING_ENABLED` | `true` |

**What is collected:**

- **Resource attributes**: Database type (`sqlite`/`postgres`/`mysql`), lock type (`local`/`redis`), cluster UUID
- **Metrics** (hourly): Total cache size, upstream count, upstream health
- **Logs**: Startup events, panic/crash events with stack traces

**What is NOT collected:**

- No personal information (usernames, emails, PII)
- No network information (IP addresses, hostnames)
- No cache contents (store paths, packages)
- No configuration secrets (passwords, keys)
- No request logs (HTTP requests, clients)

**Privacy:**

- Fully anonymous and privacy-focused
- Data sent to `otlp.ncps.dev:443` via HTTPS
- Helps maintainers understand usage patterns and prioritize development
- Easy opt-out with `--analytics-reporting-enabled=false`

**Enable (default):**

```bash
ncps serve --analytics-reporting-enabled=true
```

**Disable (opt-out):**

```bash
ncps serve --analytics-reporting-enabled=false
```

**Configuration file:**

```yaml
analytics:
  reporting:
    enabled: false  # Disable analytics
```

See [Analytics Configuration](analytics.md) for comprehensive details on what data is collected, privacy guarantees, and how it works.

## Observability

### Prometheus

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--prometheus-enabled` | Enable Prometheus /metrics endpoint | `PROMETHEUS_ENABLED` | `false` |

**Example:**

```bash
ncps serve --prometheus-enabled=true
```

Metrics available at `http://your-ncps:8501/metrics`.

### OpenTelemetry

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--otel-enabled` | Enable OpenTelemetry (logs, metrics, tracing) | `OTEL_ENABLED` | `false` |
| `--otel-grpc-url` | gRPC collector endpoint (omit for stdout) | `OTEL_GRPC_URL` | - |

**Example:**

```bash
ncps serve \
  --otel-enabled=true \
  --otel-grpc-url=http://otel-collector:4317
```

### Logging

| Option | Description | Environment Variable | Default |
|--------|-------------|---------------------|---------|
| `--log-level` | Log level: debug, info, warn, error | `LOG_LEVEL` | `info` |

See [Observability Configuration](observability.md) for details.

## Configuration File Format

All options can be specified in a configuration file. Example `config.yaml`:

```yaml
log-level: info

server:
  addr: ":8501"

cache:
  hostname: cache.example.com

  storage:
    local: /var/lib/ncps
    # OR for S3:
    # s3:
    #   bucket: ncps-cache
    #   endpoint: https://s3.amazonaws.com  # Scheme (https://) is required
    #   region: us-east-1
    #   access-key-id: ${S3_ACCESS_KEY}
    #   secret-access-key: ${S3_SECRET_KEY}
    #   force-path-style: false  # Set to true for MinIO

  database-url: sqlite:/var/lib/ncps/db/db.sqlite
  max-size: 50G
  temp-path: /tmp/ncps

  lru:
    schedule: "0 2 * * *"

  upstream:
    urls:
      - https://cache.nixos.org
      - https://nix-community.cachix.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
      - nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=

  # For HA deployments:
  redis:
    addrs:
      - redis:6379
    password: ${REDIS_PASSWORD}

  lock:
    download-lock-ttl: 5m
    lru-lock-ttl: 30m
    redis:
      key-prefix: "ncps:lock:"
    postgres:
      key-prefix: "ncps:lock:"
    retry:
      max-attempts: 3
      initial-delay: 100ms
      max-delay: 2s
      jitter: true

prometheus:
  enabled: true

otel:
  enabled: false
  grpc-url: ""
```

**Environment variable expansion:**

- Use `${VAR_NAME}` syntax in configuration files
- Variables are expanded when the config is loaded

See [config.example.yaml](../../config.example.yaml) for a complete example.

## Related Documentation

- [Storage Configuration](storage.md) - Storage backend details
- [Database Configuration](database.md) - Database backend details
- [Observability Configuration](observability.md) - Monitoring and logging
- [High Availability Guide](../deployment/high-availability.md) - HA configuration
- [Distributed Locking Guide](../deployment/distributed-locking.md) - Lock tuning
