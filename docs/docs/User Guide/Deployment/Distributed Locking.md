# Distributed Locking

This document provides comprehensive guidance on using distributed locking in ncps for high-availability deployments.

## Overview

ncps supports running multiple instances in a high-availability configuration using **Redis** for distributed locking. This enables:

- **Zero-downtime deployments** - Update instances one at a time
- **Horizontal scaling** - Add instances to handle more traffic
- **Load distribution** - Spread requests across multiple servers
- **Geographic distribution** - Deploy instances closer to clients

### Key Concepts

- **Download Deduplication**: When multiple instances request the same package, only one downloads it while others wait for the result
- **LRU Coordination**: Only one instance runs cache cleanup at a time to prevent conflicts
- **Retry with Backoff**: Failed lock acquisitions retry automatically with exponential backoff and jitter
- **Lock Expiry**: All locks have TTLs to prevent deadlocks from instance failures

1. **Local Locks** (default) - In-memory locks using Go's `sync.Mutex`, suitable for single-instance deployments
1. **Redis** - Distributed locks using the Redlock algorithm, ideal for HA deployments with existing Redis infrastructure

## Architecture

### Single-Instance Mode (Default)

```
┌──────────────────┐
│                  │
│   ncps Instance  │
│                  │
│   Local Locks    │
│   (sync.Mutex)   │
│                  │
└────────┬─────────┘
         │
         ├─> Local Storage or S3
         └─> SQLite Database
```

- Uses Go's `sync.Mutex` and `sync.RWMutex` for coordination
- No external dependencies required
- Suitable for single-server deployments

### High-Availability Mode

```
                    ┌──────────┐
                    │   Load   │
                    │ Balancer │
                    └────┬─────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
┌────────────────┐ ┌────────────────┐ ┌────────────────┐
│ ncps Instance  │ │ ncps Instance  │ │ ncps Instance  │
│ #1             │ │ #2             │ │ #3             │
│                │ │                │ │                │
│ Redis Locks    │ │ Redis Locks    │ │ Redis Locks    │
│ (Redlock)      │ │ (Redlock)      │ │ (Redlock)      │
└────┬───────────┘ └────┬───────────┘ └────┬───────────┘
     │                  │                  │
     │                  │                  │
     ├──────────────────┼──────────────────┤
     │                  │                  │
     │   ┌──────────────▼──────────┐       │
     │   │                         │       │
     ├──►│    Redis Server         │◄──────┘
     │   │  (Distributed Locks)    │
     │   └─────────────────────────┘
     │
     ├──────────────┐
     │              │
     ▼              ▼
┌─────────┐  ┌───────────┐
│   S3    │  │PostgreSQL │
│ Storage │  │ Database  │
└─────────┘  └───────────┘
```

- All instances share the same S3 storage and database
- Redis coordinates locks across instances
- Load balancer distributes traffic
- Instances can be added/removed dynamically

```
                    ┌──────────┐
                    │   Load   │
                    │ Balancer │
                    └────┬─────┘
                         │
        ┌────────────────┼────────────────┐
        │                │                │
        ▼                ▼                ▼
┌────────────────┐ ┌────────────────┐ ┌────────────────┐
│ ncps Instance  │ │ ncps Instance  │ │ ncps Instance  │
│ #1             │ │ #2             │ │ #3             │
│                │ │                │ │                │
│ DB Adv. Locks  │ │ DB Adv. Locks  │ │ DB Adv. Locks  │
└────┬───────────┘ └────┬───────────┘ └────┬───────────┘
     │                  │                  │
     │                  │                  │
     ├──────────────────┼──────────────────┤
     │                  │                  │
     │   ┌──────────────▼──────────┐       │
     │   │                         │       │
     ├──►│  PostgreSQL Database    │◄──────┘
     │   │ (Data + Advisory Locks) │
     │   └─────────────────────────┘
     │
     │
     │
     ▼
┌─────────┐
│   S3    │
│ Storage │
└─────────┘
```

## When to Use Distributed Locking

### Use Distributed Locking When:

✅ **Running Multiple Instances**

- Deploying behind a load balancer
- Need zero-downtime updates
- Require failover capability

✅ **High Traffic Environments**

- Handling thousands of concurrent requests
- Need horizontal scaling
- Geographic distribution required

✅ **Production Deployments**

- Business-critical caching infrastructure
- SLA requirements for availability
- Need redundancy and fault tolerance

### Use Local Locking When:

✅ **Single Server Deployment**

- Running on a single machine
- No redundancy required
- Development/testing environments

✅ **Low Traffic Environments**

- Small teams or personal use
- Limited concurrent requests
- Resource-constrained environments

✅ **Simplified Operations**

- Want minimal infrastructure
- Distributed lock backend is not necessary
- Prefer embedded solutions (SQLite)

## Configuration Guide

### Prerequisites

For high-availability mode, you need:

1. **Distributed Lock Backend** (one of the following):
   - **Redis** (version 5.0 or later)
1. **Shared Storage** (S3-compatible)
   - AWS S3, MinIO, DigitalOcean Spaces, etc.
   - All instances must access the same bucket
1. **Shared Database** (PostgreSQL or MySQL)
   - PostgreSQL 12+ or MySQL 8.0+ recommended
   - All instances must connect to the same database
   - SQLite is NOT supported in HA mode

### Basic Configuration

**Minimum configuration for HA:**

```
ncps serve \
  --cache-hostname=cache.example.com \
  --cache-database-url=postgresql://user:pass@postgres:5432/ncps \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-region=us-east-1 \
  --cache-redis-addrs=redis:6379
```

**Configuration file (config.yaml):**

```yaml
cache:
  hostname: cache.example.com
  database-url: postgresql://user:pass@postgres:5432/ncps

  storage:
    s3:
      bucket: ncps-cache
      region: us-east-1
      endpoint: https://s3.amazonaws.com

  redis:
    addrs:
      - redis:6379
```

### Lock Backend Selection

The `--cache-lock-backend` flag determines which mechanism ncps uses to coordinate locks across instances.

| Option | Description | Default |
| --- | --- | --- |
| `--cache-lock-backend` | Lock backend: `local` or `redis` | `local` |

- **local**: Uses in-memory locks. Only suitable for single-instance deployments.
- **redis**: Uses Redis (Redlock algorithm). Best for high-traffic, multi-instance deployments.

### Redis Configuration Options

#### Connection Settings

| Option | Description | Default |
| --- | --- | --- |
| `--cache-redis-addrs` | Comma-separated Redis addresses | (none - local mode) |
| `--cache-redis-username` | Username for Redis ACL | "" |
| `--cache-redis-password` | Password for authentication | "" |
| `--cache-redis-db` | Database number (0-15) | 0 |
| `--cache-redis-use-tls` | Enable TLS connections | false |
| `--cache-redis-pool-size` | Connection pool size | 10 |

#### Redis Lock Settings

| Option | Description | Default |
| --- | --- | --- |
| `--cache-lock-redis-key-prefix` | Key prefix for all locks | "ncps:lock:" |

#### Lock Timing Settings

| Option | Description | Default |
| --- | --- | --- |
| `--cache-lock-download-ttl` | Download lock timeout | 5m |
| `--cache-lock-lru-ttl` | LRU lock timeout | 30m |

#### Retry Configuration

| Option | Description | Default |
| --- | --- | --- |
| `--cache-lock-retry-max-attempts` | Maximum retry attempts | 3 |
| `--cache-lock-retry-initial-delay` | Initial retry delay | 100ms |
| `--cache-lock-retry-max-delay` | Maximum backoff delay | 2s |
| `--cache-lock-retry-jitter` | Enable jitter in retries | true |

### Advanced Configuration

**Redis Cluster:**

```
--cache-redis-addrs=node1:6379,node2:6379,node3:6379
```

**TLS with Authentication:**

```
--cache-redis-use-tls=true \
--cache-redis-username=ncps \
--cache-redis-password=secret \
--cache-redis-addrs=redis.example.com:6380
```

**Custom Lock Timeouts:**

```
--cache-lock-download-ttl=10m \
--cache-lock-lru-ttl=1h \
--cache-lock-retry-max-attempts=5 \
--cache-lock-retry-max-delay=5s
```

## How It Works

### Lock Types

ncps uses two types of distributed locks:

#### 1. Download Locks (Exclusive)

**Purpose:** Prevent duplicate downloads of the same package

**Lock Key Pattern:** `ncps:lock:download:nar:{hash}` or `ncps:lock:download:narinfo:{hash}`

**Behavior:**

- When instance A starts downloading a package, it acquires the lock
- Instance B requesting the same package will retry acquiring the lock
- Once instance A completes the download, it releases the lock
- Instance B then reads the package from shared storage (no download needed)

**TTL:** 5 minutes (configurable via `--cache-lock-download-ttl`)

#### 2. LRU Lock (Exclusive)

**Purpose:** Coordinate cache cleanup across instances

**Lock Key Pattern:** `ncps:lock:lru`

**Behavior:**

- Uses `TryLock()` - non-blocking acquisition
- If locked, the instance skips LRU and tries again next cycle
- Only one instance runs LRU cleanup at a time
- Prevents concurrent deletions that could corrupt the cache

**TTL:** 30 minutes (configurable via `--cache-lock-lru-ttl`)

### Redlock Algorithm

ncps uses the [Redlock algorithm](https://redis.io/docs/latest/develop/clients/patterns/distributed-locks/) via [go-redsync](https://github.com/go-redsync/redsync):

1. **Acquire**: Try to SET with NX (only if not exists) and PX (expiry time)
1. **Release**: Delete the key when operation completes
1. **Expire**: Lock auto-expires after TTL if instance crashes (important for long-running downloads)

**Note**: Lock extension is not currently implemented. The TTL should be set long enough to accommodate the longest expected download operation.

### Retry Strategy

When a lock is already held:

```
Attempt 1: Wait 100ms + random jitter (0-100ms)
Attempt 2: Wait 200ms + random jitter (0-200ms)
Attempt 3: Wait 400ms + random jitter (0-400ms)
... up to max-delay (2s by default)
```

**Exponential Backoff Formula:**

```
delay = min(initial_delay * 2^attempt, max_delay)
actual_delay = delay + random(0, delay * jitter_factor)
```

**Why Jitter?**

- Prevents thundering herd when lock is released
- Distributes retry attempts across time
- Improves fairness in lock acquisition

### Cache Access Protection

Read operations (GetNar, GetNarInfo) acquire read locks to prevent the LRU from deleting files while they're being served:

```
┌──────────────┐
│ HTTP Request │
└──────┬───────┘
       │
       ▼
┌──────────────────┐
│ Acquire RLock    │ ◄─ Allows concurrent reads
│ (cache)          │    Blocks LRU deletes
└──────┬───────────┘
       │
       ▼
┌──────────────────┐
│ Serve File       │
└──────┬───────────┘
       │
       ▼
┌──────────────────┐
│ Release RLock    │
└──────────────────┘
```

## Monitoring and Observability

### Key Metrics (Coming Soon)

The following OpenTelemetry metrics will be available for monitoring lock operations:

- `ncps_lock_acquisitions_total{type,result,mode}` - Lock acquisition attempts
  - `type`: "download" or "lru"
  - `result`: "success" or "failure"
  - `mode`: "local" or "distributed"
- `ncps_lock_hold_duration_seconds{type,mode}` - Time locks are held
  - Histogram of lock hold times
  - Helps identify slow operations
- `ncps_lock_failures_total{type,reason,mode}` - Lock failures
  - `reason`: "timeout", "redis_error", "circuit_breaker"
  - Indicates infrastructure issues
- `ncps_lock_retry_attempts_total{type}` - Retry attempts before success/failure
  - Shows lock contention levels
  - High values indicate scaling needs

### Logging

ncps logs lock operations with structured fields:

```
{
  "level": "info",
  "msg": "acquired download lock",
  "hash": "abc123...",
  "lock_type": "download:nar",
  "duration_ms": 150,
  "retries": 2
}
```

**Important log messages:**

- `acquired download lock` - Successfully acquired lock for download
- `failed to acquire lock` - Lock acquisition failed after retries
- `another instance is running LRU` - LRU skipped (another instance running)
- `circuit breaker open: Redis is unavailable` - Redis connectivity issues

### Health Checks

Monitor Redis health:

```
# Check Redis connectivity
redis-cli -h redis-host ping
# Expected: PONG

# Check lock keys
redis-cli -h redis-host --scan --pattern "ncps:lock:*"
# Shows active locks

# Monitor lock key expiry
redis-cli -h redis-host TTL "ncps:lock:download:nar:abc123"
# Shows remaining TTL in seconds
```

## Troubleshooting

### Download Locks Not Working

**Symptom:** Multiple instances download the same package

**Diagnosis:**

```
# Check if instances are using Redis
grep "using local locks" /var/log/ncps.log
# Should NOT appear if Redis is configured

# Check Redis connectivity from each instance
redis-cli -h $REDIS_HOST ping

# Monitor lock acquisitions
grep "acquired download lock" /var/log/ncps.log | wc -l
```

**Common Causes:**

1. **Redis not configured** - Verify `--cache-redis-addrs` is set
1. **Network issues** - Check firewall rules, DNS resolution
1. **Redis authentication** - Verify username/password if ACL is enabled
1. **Different key prefixes** - Ensure all instances use the same `--cache-lock-redis-key-prefix`

**Solution:**

```
# Test Redis connectivity
telnet redis-host 6379

# Verify configuration
ncps serve --help | grep redis

# Check Redis logs
tail -f /var/log/redis/redis-server.log
```

### High Lock Contention

**Symptom:** Many retry attempts, slow downloads

**Diagnosis:**

```
# Monitor retry attempts
grep "retries" /var/log/ncps.log | awk '{sum+=$NF; count++} END {print sum/count}'
# Average retries per lock acquisition

# Check active locks
redis-cli --scan --pattern "ncps:lock:download:*" | wc -l
```

**Solutions:**

1. **Increase retry settings:**

   ```
   --cache-lock-retry-max-attempts=5 \
   --cache-lock-retry-max-delay=5s
   ```

1. **Scale down instances** (if too many instances competing)

1. **Increase lock TTL** for long-running operations:

   ```
   --cache-lock-download-ttl=10m
   ```

### LRU Not Running

**Symptom:** Cache grows beyond max-size, old files not deleted

**Diagnosis:**

```
# Check for LRU lock acquisition messages
grep "running LRU" /var/log/ncps.log

# Check for skip messages
grep "another instance is running LRU" /var/log/ncps.log

# Verify LRU lock status
redis-cli GET "ncps:lock:lru"
```

**Common Causes:**

1. **LRU lock stuck** - Lock held by crashed instance
1. **All instances skipping** - Each thinks another is running

**Solution:**

```
# Manually release stuck LRU lock
redis-cli DEL "ncps:lock:lru"

# Restart all instances to reset state
systemctl restart ncps@*
```

### Redis Connection Failures

**Symptom:** Logs show "circuit breaker open" or "Redis is unavailable"

**Diagnosis:**

```
# Check Redis status
systemctl status redis

# Test connectivity
redis-cli -h $REDIS_HOST -p 6379 ping

# Check network
nc -zv redis-host 6379
```

**Solutions:**

1. **Verify Redis is running:**

   ```
   systemctl start redis
   ```

1. **Check firewall rules:**

   ```
   sudo iptables -L | grep 6379
   ```

1. **Verify TLS configuration** if using `--cache-redis-use-tls`:

   ```
   openssl s_client -connect redis-host:6380
   ```

## Performance Tuning

### Lock TTLs

**Download Lock TTL** (`--cache-lock-download-ttl`):

- **Default:** 5 minutes
- **Increase if:** Large packages take longer to download
- **Decrease if:** Most downloads complete quickly (reduces stuck lock impact)

**LRU Lock TTL** (`--cache-lock-lru-ttl`):

- **Default:** 30 minutes
- **Increase if:** LRU cleanup takes longer (very large caches)
- **Decrease if:** Want faster failover if instance crashes during LRU

### Retry Configuration

**Max Attempts** (`--cache-lock-retry-max-attempts`):

- **Default:** 3
- **Increase if:** High lock contention (many instances)
- **Decrease if:** Want faster failure feedback

**Initial Delay** (`--cache-lock-retry-initial-delay`):

- **Default:** 100ms
- **Increase if:** Redis is slow or distant
- **Decrease if:** Redis is fast and local

**Max Delay** (`--cache-lock-retry-max-delay`):

- **Default:** 2s
- **Increase if:** Locks are held for long periods
- **Decrease if:** Want faster failure

### Redis Pool Size

**Pool Size** (`--cache-redis-pool-size`):

- **Default:** 10 connections per instance
- **Increase if:** High concurrent download requests
- **Decrease if:** Running many instances (to reduce Redis load)

**Formula:** `total_connections = instances * pool_size`

**Redis max connections:** Usually 10,000 by default

## Best Practices

### Deployment

1. **Start Redis First**
   - Ensure Redis is healthy before starting ncps instances
   - Use health checks in orchestration (Kubernetes, Docker Compose)
1. **Gradual Rollout**
   - Update instances one at a time
   - Verify each instance is healthy before updating the next
   - Monitor lock metrics during rollout
1. **Load Balancer Configuration**
   - Use health check endpoint: `GET /pubkey`
   - Configure session affinity if needed (not required)
   - Set reasonable timeouts (downloads can be large)
1. **Shared Storage**
   - Ensure all instances have identical S3 configuration
   - Use IAM roles or credentials with proper permissions
   - Enable S3 server-side encryption for security
1. **Database**
   - Use connection pooling in PostgreSQL
   - Configure appropriate timeouts
   - Monitor connection counts

### Monitoring

1. **Key Metrics to Watch**
   - Lock acquisition latency
   - Retry attempt rates
   - Redis connectivity
   - Cache hit rates
1. **Alerting**
   - Alert on high lock failures
   - Alert on Redis unavailability
   - Alert on excessive retry attempts
1. **Logging**
   - Centralize logs (ELK, Loki, CloudWatch)
   - Include structured fields for filtering
   - Set appropriate log levels

### Operations

1. **Backup Strategy**
   - Redis: Optional (locks are ephemeral)
   - Database: Regular backups (contains metadata)
   - S3: Enable versioning for disaster recovery
1. **Scaling**
   - Add instances during high traffic
   - Remove instances during maintenance
   - Monitor for lock contention when scaling
1. **Maintenance**
   - Update one instance at a time
   - Redis can be restarted (locks will regenerate)
   - Database migrations should be backward-compatible

## Migration Guide

### From Single Instance to HA

**Prerequisites:**

1. ✅ Set up PostgreSQL database
1. ✅ Migrate from SQLite (if applicable)
1. ✅ Set up S3-compatible storage
1. ✅ Deploy Redis server

**Migration Steps:**

1. **Migrate to S3 Storage:**

   ```
   # Sync local storage to S3
   aws s3 sync /var/lib/ncps/storage s3://ncps-cache/
   ```

1. **Migrate Database:**

   ```
   # Export SQLite data
   sqlite3 ncps.db .dump > backup.sql

   # Import to PostgreSQL (after schema conversion)
   psql ncps < converted.sql
   ```

1. **Configure First Instance:**

   ```
   ncps serve \
     --cache-database-url=postgresql://... \
     --cache-storage-s3-bucket=ncps-cache \
     --cache-redis-addrs=redis:6379
   ```

1. **Verify Functionality:**

   - Test package downloads
   - Check Redis for lock keys
   - Verify cache hits

1. **Add Additional Instances:**

   - Use identical configuration
   - Point to same Redis, S3, and database
   - Add to load balancer

### Rollback Plan

If issues occur:

1. **Stop new instances** (keep first instance)

1. **Continue using first instance** with Redis

1. **Or temporarily disable Redis:**

   ```
   # Remove --cache-redis-addrs flag
   # Falls back to local locks
   ```

**Note:** Rollback from S3 to local storage requires data sync:

```
aws s3 sync s3://ncps-cache/ /var/lib/ncps/storage
```

______________________________________________________________________

## Additional Resources

- **Redis Official Documentation:** [https://redis.io/docs/](https://redis.io/docs/)
- **Redlock Algorithm:** [https://redis.io/docs/latest/develop/clients/patterns/distributed-locks/](https://redis.io/docs/latest/develop/clients/patterns/distributed-locks/)
- **go-redsync Library:** [https://github.com/go-redsync/redsync](https://github.com/go-redsync/redsync)
- **ncps Configuration Reference:** See [`config.example.yaml`](https://github.com/kalbasit/ncps/blob/main/config.example.yaml)
- **High Availability Best Practices:** AWS Well-Architected Framework

## Support

For issues or questions:

- **GitHub Issues:** [https://github.com/kalbasit/ncps/issues](https://github.com/kalbasit/ncps/issues)
- **Discussions:** [https://github.com/kalbasit/ncps/discussions](https://github.com/kalbasit/ncps/discussions)
- **CONTRIBUTING.md:** Development and testing guide
