# High Availability

## High Availability Deployment

Deploy multiple ncps instances for zero-downtime operation, load distribution, and redundancy.

## Why High Availability?

Running multiple ncps instances provides:

- ✅ **Zero Downtime** - Instance failures don't interrupt service
- ✅ **Load Distribution** - Requests spread across multiple servers
- ✅ **Horizontal Scaling** - Add instances to handle more traffic
- ✅ **Geographic Distribution** - Deploy instances closer to clients
- ✅ **Rolling Updates** - Update instances one at a time without downtime

## Architecture

```
┌──────────────────────────────────┐
│        Nix Clients               │
└────────────────┬─────────────────┘
                 │
                 ▼
┌────────────────────────────────┐
│      Load Balancer             │
│  (nginx, HAProxy, cloud LB)    │
└────────┬────────────────┬──────┘
         │                │
    ┌────▼────┐      ┌────▼────┐      ┌──────────┐
    │ ncps #1 │      │ ncps #2 │  ... │ ncps #N  │
    └────┬────┘      └────┬────┘      └─────┬────┘
         │                │                 │
         └────────┬───────┴─────────────────┘
                  │
      ┌───────────┼────────────┬───────────┐
      │           │            │           │
      ▼           ▼            ▼           ▼
    ┌──────────┐ ┌────────┐  ┌──────────┐ ┌─────────┐
    │  Redis / │ │   S3   │  │PostgreSQL│ │  Load   │
    │ Database │ │Storage │  │ / MySQL  │ │Balancer │
    │ (Locks)  │ │        │  │ (Data)   │ │         │
    └──────────┘ └────────┘  └──────────┘ └─────────┘
```

## Requirements

### Required Components

1. **Multiple ncps instances** (2+, recommended 3+)
1. **Distributed locking backend**
   - **Redis server** (version 5.0+)
   - **PostgreSQL advisory locks** (version 9.1+)
1. **S3-compatible storage** (shared across all instances)
   - AWS S3, MinIO, DigitalOcean Spaces, etc.
1. **PostgreSQL or MySQL database** (shared across all instances)
   - PostgreSQL 12+ or MySQL 8.0+
   - **SQLite is NOT supported for HA**
1. **Load balancer** to distribute requests
   - nginx, HAProxy, cloud load balancer, etc.

### Network Requirements

- All instances must reach Redis
- All instances must reach S3 storage
- All instances must reach shared database
- Load balancer must reach all instances
- Clients reach load balancer

## Quick Start

### Option 1: Docker Compose with MinIO

See [Docker Compose HA example](../Installation/Docker%20Compose.md).

### Option 2: Kubernetes with Helm

```
helm install ncps ./charts/ncps -n ncps -f values-ha.yaml
```

`values-ha.yaml`:

```yaml
replicaCount: 3

  hostname: cache.example.com
  hostName: cache.example.com

  storage:
    s3:
      enabled: true
      bucket: ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      forcePathStyle: false  # Set to true for MinIO

  database:
    url: postgresql://ncps:password@postgres:5432/ncps

  redis:
    enabled: true
    addrs:
      - redis:6379

podDisruptionBudget:
  enabled: true
  minAvailable: 2
```

See <a class="reference-link" href="../Installation/Helm%20Chart.md">Helm Chart</a> for details.

## Detailed Configuration

### Step 1: Set Up Redis

**Single Redis Instance:**

```
docker run -d \
  --name redis \
  -p 6379:6379 \
  redis:7-alpine
```

**Redis Cluster** (for production):

```yaml
# Use Redis cluster or sentinel for Redis HA
# See Redis documentation for cluster setup
```

### Step 2: Set Up S3 Storage

**AWS S3:**

```
# Create bucket
aws s3 mb s3://ncps-cache --region us-east-1

# Enable versioning (recommended)
aws s3api put-bucket-versioning \
  --bucket ncps-cache \
  --versioning-configuration Status=Enabled
```

**MinIO:**

```
# Start MinIO
docker run -d \
  --name minio \
  -p 9000:9000 \
  -p 9001:9001 \
  -v minio-data:/data \
  minio/minio server /data --console-address ":9001"

# Create bucket
mc alias set myminio http://localhost:9000 minioadmin minioadmin
mc mb myminio/ncps-cache
```

### Step 3: Set Up Database

**PostgreSQL:**

```
# Create database and user
sudo -u postgres psql
```

```
CREATE DATABASE ncps;
CREATE USER ncps WITH PASSWORD 'secure-password';
GRANT ALL PRIVILEGES ON DATABASE ncps TO ncps;
```

**MySQL:**

```
CREATE DATABASE ncps;
CREATE USER 'ncps'@'%' IDENTIFIED BY 'secure-password';
GRANT ALL PRIVILEGES ON ncps.* TO 'ncps'@'%';
FLUSH PRIVILEGES;
```

### Step 4: Configure ncps Instances

All instances use **identical configuration**:

```yaml
cache:
  hostname: cache.example.com  # Same for all instances

  storage:
    s3:
      bucket: ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      access-key-id: ${S3_ACCESS_KEY}
      secret-access-key: ${S3_SECRET_KEY}
      force-path-style: false  # Set to true for MinIO

  database-url: postgresql://ncps:password@postgres:5432/ncps?sslmode=require

  redis:
    addrs:
      - redis:6379
    password: ${REDIS_PASSWORD}  # If using auth

  lock:
    backend: redis  # Options: local, redis, postgres, mysql
    download-lock-ttl: 5m
    lru-lock-ttl: 30m
    retry:
      max-attempts: 3
      initial-delay: 100ms
      max-delay: 2s
      jitter: true

  upstream:
    urls:
      - https://cache.nixos.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=

prometheus:
  enabled: true
```

### Step 5: Deploy Instances

**Docker:**

```sh
# Start instance 1
docker run -d --name ncps-1 -p 8501:8501 \
  -v $(pwd)/config.yaml:/config.yaml \
  ghcr.io/kalbasit/ncps /bin/ncps serve --config=/config.yaml

# Start instance 2
docker run -d --name ncps-2 -p 8502:8501 \
  -v $(pwd)/config.yaml:/config.yaml \
  ghcr.io/kalbasit/ncps /bin/ncps serve --config=/config.yaml

# Start instance 3
docker run -d --name ncps-3 -p 8503:8501 \
  -v $(pwd)/config.yaml:/config.yaml \
  ghcr.io/kalbasit/ncps /bin/ncps serve --config=/config.yaml
```

**Kubernetes:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ncps
spec:
  replicas: 3
  selector:
    matchLabels:
      app: ncps
  template:
    metadata:
      labels:
        app: ncps
    spec:
      containers:
        - name: ncps
          image: ghcr.io/kalbasit/ncps:latest
          # ... configuration ...
```

### Step 6: Set Up Load Balancer

**nginx:**

```
upstream ncps_backend {
    server ncps-1:8501;
    server ncps-2:8501;
    server ncps-3:8501;
}

server {
    listen 80;
    server_name cache.example.com;

    location / {
        proxy_pass http://ncps_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

**HAProxy:**

```
frontend ncps_frontend
    bind *:80
    default_backend ncps_backend

backend ncps_backend
    balance roundrobin
    option httpchk GET /nix-cache-info
    server ncps1 ncps-1:8501 check
    server ncps2 ncps-2:8501 check
    server ncps3 ncps-3:8501 check
```

## How Distributed Locking Works

ncps uses Redis to coordinate multiple instances:

### Download Deduplication

When multiple instances request the same package:

1. Instance A acquires download lock for hash `abc123`
1. Instance B tries to download same package
1. Instance B cannot acquire lock (Instance A holds it)
1. Instance B retries with exponential backoff
1. Instance A completes download and releases lock
1. Instance B acquires lock, finds package in S3, serves it
1. Result: Only one download from upstream

### LRU Coordination

Only one instance runs cache cleanup at a time:

1. Instances try to acquire global LRU lock
1. First instance to acquire lock runs LRU
1. Other instances skip LRU (lock held)
1. After cleanup, lock is released
1. Next scheduled LRU cycle, another instance may acquire lock

**Benefits:**

- Prevents concurrent deletions
- Avoids cache corruption
- Distributes LRU load

See <a class="reference-link" href="Distributed%20Locking.md">Distributed Locking</a> for technical details and database advisory lock configuration (PostgreSQL).

## Health Checks

Configure load balancer health checks:

**Endpoint:** `GET /nix-cache-info`

**nginx example:**

```
upstream ncps_backend {
    server ncps-1:8501 max_fails=3 fail_timeout=30s;
    server ncps-2:8501 max_fails=3 fail_timeout=30s;
    server ncps-3:8501 max_fails=3 fail_timeout=30s;
}
```

**Kubernetes:**

```yaml
livenessProbe:
  httpGet:
    path: /nix-cache-info
    port: 8501
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /nix-cache-info
    port: 8501
  initialDelaySeconds: 5
  periodSeconds: 5
```

## Rolling Updates

Update instances one at a time for zero downtime:

```
# Update instance 1
docker stop ncps-1
docker rm ncps-1
docker pull ghcr.io/kalbasit/ncps:latest
docker run -d --name ncps-1 ... # Same command

# Wait and verify instance 1 is healthy

# Update instance 2
docker stop ncps-2
# ... same process

# Update instance 3
# ... same process
```

**Kubernetes:**

```yaml
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
      maxSurge: 1
```

## Monitoring HA Deployments

### Key Metrics

- **Instance health**: Up/down status
- **Lock acquisition rate**: Download and LRU locks
- **Lock contention**: Retry attempts
- **Redis connectivity**: Connection status
- **Cache hit rate**: Per-instance and aggregate

### Example Prometheus Queries

```
# Lock acquisition success rate
rate(ncps_lock_acquisitions_total{result="success"}[5m])
/ rate(ncps_lock_acquisitions_total[5m])

# Lock retry attempts
rate(ncps_lock_retry_attempts_total[5m])

# Cache hit rate
rate(ncps_nar_served_total[5m])
```

See <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> for dashboards.

## Troubleshooting

### Download Locks Not Working

**Symptom:** Multiple instances download the same package

**Check:**

```
# Verify Redis configuration
grep "redis-addrs" config.yaml

# Test Redis connectivity
redis-cli -h redis-host ping

# Check logs for lock messages
grep "acquired download lock" /var/log/ncps.log
```

### High Lock Contention

**Symptom:** Many retry attempts, slow downloads

**Solutions:**

1. Increase retry settings
1. Increase lock TTLs for long operations
1. Scale down instances if too many

See <a class="reference-link" href="Distributed%20Locking.md">Distributed Locking</a> for detailed troubleshooting.

## Migration from Single-Instance

### Prerequisites

1. ✅ Set up PostgreSQL or MySQL database
1. ✅ Migrate from SQLite (if applicable)
1. ✅ Set up S3-compatible storage
1. ✅ Deploy Redis server

### Migration Steps

**1. Migrate to S3 Storage:**

```
# Sync local storage to S3
aws s3 sync /var/lib/ncps s3://ncps-cache/
```

**2. Migrate Database:**

```
# Export SQLite data
sqlite3 ncps.db .dump > backup.sql

# Import to PostgreSQL (after conversion)
pgloader sqlite:///var/lib/ncps/db/db.sqlite \
  postgresql://ncps:password@localhost:5432/ncps
```

**3. Configure First Instance:**

```yaml
# Update config.yaml to use S3 and PostgreSQL
# Add Redis configuration
```

**4. Verify Functionality:**

- Test package downloads
- Check Redis for lock keys
- Verify cache hits

**5. Add Additional Instances:**

- Use identical configuration
- Point to same Redis, S3, and database
- Add to load balancer

## Best Practices

1. **Start Redis First** - Ensure Redis is healthy before starting ncps instances
1. **Use Health Checks** - Configure load balancer health checks
1. **Monitor Lock Metrics** - Watch for contention and failures
1. **Plan Capacity** - 3+ instances recommended for true HA
1. **Test Failover** - Regularly test instance failures
1. **Centralize Logs** - Use log aggregation for troubleshooting
1. **Set Up Alerts** - Alert on high lock failures, Redis unavailability

## Next Steps

1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Set up Nix clients
1. <a class="reference-link" href="Distributed%20Locking.md">Distributed Locking</a> - Understand locking in depth
1. <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Configure observability
1. <a class="reference-link" href="../Operations.md">Operations</a> - Learn about backups, upgrades

## Related Documentation

- <a class="reference-link" href="Distributed%20Locking.md">Distributed Locking</a> - Deep dive into Redis locking
- <a class="reference-link" href="../Installation/Helm%20Chart.md">Helm Chart</a> - Simplified HA deployment
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All HA options
- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - HA-specific monitoring
