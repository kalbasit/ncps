# Single Instance

## Single-Instance Deployment

Deploy ncps as a single server for simplified operations and maintenance.

## When to Use

Single-instance deployment is ideal for:

- **Development and testing environments**
- **Small to medium teams** (1-100+ users)
- **Single-location deployments**
- **Simpler operational requirements**
- **Cost-conscious deployments**

## Architecture

```
┌────────────────────────────┐
│      Nix Clients           │
│      (Multiple)            │
└─────────────┬──────────────┘
              │
              ▼
┌─────────────────────────────┐
│        ncps Server          │
│                             │
│   ┌─────────────────────┐   │
│   │   Local Locks       │   │
│   │   (sync.Mutex)      │   │
│   └─────────────────────┘   │
│                             │
│   Storage: Local or S3      │
│   Database: Any backend     │
└─────────────────────────────┘
```

## Storage Options

### Option 1: Local Filesystem Storage

**Pros:**

- Simple setup
- Fast (local I/O)
- No external dependencies

**Cons:**

- Limited to server disk size
- No redundancy
- Cannot scale to HA

**Configuration:**

```yaml
cache:
  hostname: cache.example.com
  storage:
    local: /var/lib/ncps
  database-url: sqlite:/var/lib/ncps/db/db.sqlite
```

### Option 2: S3 Storage

**Pros:**

- Scalable storage
- Easy migration to HA later
- Built-in redundancy

**Cons:**

- Requires S3 service
- Slight latency overhead
- Additional cost (if using cloud S3)

**Configuration:**

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
  database-url: sqlite:/var/lib/ncps/db/db.sqlite
```

## Database Options

### SQLite (Default - Recommended for Single-Instance)

**Pros:**

- No external service required
- Zero configuration
- Perfect for single instance

**Cons:**

- Cannot be used for HA
- Single connection limit

**Configuration:**

```yaml
cache:
  database-url: sqlite:/var/lib/ncps/db/db.sqlite
```

### PostgreSQL

**Pros:**

- Better performance under load
- Easier migration to HA later
- Better concurrent access

**Cons:**

- Requires PostgreSQL service
- More complex setup

**Configuration:**

```yaml
cache:
  database-url: postgresql://ncps:password@localhost:5432/ncps?sslmode=require
```

### MySQL/MariaDB

Similar to PostgreSQL in terms of pros/cons.

**Configuration:**

```yaml
cache:
  database-url: mysql://ncps:password@localhost:3306/ncps
```

## Complete Configuration Examples

### Minimal (Local Storage + SQLite)

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

server:
  addr: "0.0.0.0:8501"
```

### Production-Ready (S3 + PostgreSQL)

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

  database-url: postgresql://ncps:${DB_PASSWORD}@postgres:5432/ncps?sslmode=require

  max-size: 100G
  lru:
    schedule: "0 2 * * *"  # Daily at 2 AM

  upstream:
    urls:
      - https://cache.nixos.org
      - https://nix-community.cachix.org
    public-keys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
      - nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=

server:
  addr: "0.0.0.0:8501"

prometheus:
  enabled: true
```

## Resource Requirements

### Minimum

- **CPU**: 2 cores
- **RAM**: 4GB
- **Disk**: 50GB (for local storage)
- **Network**: 100Mbps

### Recommended

- **CPU**: 4+ cores
- **RAM**: 8GB+
- **Disk**: 200GB-1TB (for local storage)
- **Network**: 1Gbps

## Installation Methods

Choose your preferred installation method:

- <a class="reference-link" href="../Installation/Docker.md">Docker</a> - Quickest setup
- <a class="reference-link" href="../Installation/Docker%20Compose.md">Docker Compose</a> - Automated deployment
- <a class="reference-link" href="../Installation/NixOS.md">Nixos</a> - Native NixOS integration
- <a class="reference-link" href="../Installation/Kubernetes.md">Kubernetes</a> - For K8s environments
- <a class="reference-link" href="../Installation/Helm%20Chart.md">Helm</a> - Simplified K8s deployment

## Deployment Checklist

- [ ] Choose storage backend (local or S3)
- [ ] Choose database backend (SQLite, PostgreSQL, or MySQL)
- [ ] Provision server/VM with sufficient resources
- [ ] Configure network and firewall rules (port 8501)
- [ ] Set up storage (create directories or S3 bucket)
- [ ] Set up database (create database and user if not SQLite)
- [ ] Deploy ncps using chosen installation method
- [ ] Configure LRU cleanup schedule
- [ ] Enable Prometheus metrics (recommended)
- [ ] Verify deployment (test cache info endpoint)
- [ ] Configure clients to use the cache
- [ ] Set up monitoring and alerts (recommended)
- [ ] Configure backups (database and optionally storage)

## Post-Deployment

### Verify Installation

```
# Check service is running
curl http://your-ncps:8501/nix-cache-info

# Get public key
curl http://your-ncps:8501/pubkey

# Check metrics (if enabled)
curl http://your-ncps:8501/metrics
```

### Configure Clients

See <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a>.

### Set Up Monitoring

See <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a>.

## Limitations

Single-instance deployments have these limitations:

- **No redundancy**: Server downtime = cache downtime
- **No load distribution**: One server handles all requests
- **No zero-downtime updates**: Updates require service restart
- **Limited scalability**: Cannot add more instances

**When you outgrow single-instance:** See <a class="reference-link" href="High%20Availability.md">High Availability</a> for migration.

## Troubleshooting

### Service Won't Start

```
# Check logs
journalctl -u ncps -f  # systemd
docker logs ncps       # Docker

# Common issues:
# - Missing required flags (hostname, storage, upstream)
# - Database not initialized
# - Permission errors
```

### Performance Issues

```
# Check disk space
df -h

# Check memory usage
free -h

# Monitor cache hit rate in logs or metrics
```

### Storage Full

```
# Enable LRU cleanup
--cache-max-size=100G
--cache-lru-schedule="0 2 * * *"

# Manually clean up (DANGER: deletes cache)
rm -rf /var/lib/ncps/nar/*
```

See the <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> for more help.

## Next Steps

1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Set up Nix clients
1. <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Set up observability
1. <a class="reference-link" href="../Operations.md">Operations</a> - Learn about backups, upgrades, etc.
1. **Consider** <a class="reference-link" href="High%20Availability.md">High Availability</a> - When you need to scale

## Related Documentation

- <a class="reference-link" href="../Installation.md">Installation</a> - Installation methods
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All configuration options
- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Storage backend details
- <a class="reference-link" href="../Configuration/Database.md">Database</a> - Database backend details
- <a class="reference-link" href="High%20Availability.md">High Availability</a> - Migrate to HA when needed
