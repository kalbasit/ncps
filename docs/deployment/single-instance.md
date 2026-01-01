[Home](../../README.md) > [Documentation](../README.md) > [Deployment](README.md) > Single-Instance

# Single-Instance Deployment

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
      endpoint: s3.amazonaws.com
      region: us-east-1
      access-key-id: ${S3_ACCESS_KEY}
      secret-access-key: ${S3_SECRET_KEY}
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
      endpoint: s3.amazonaws.com
      region: us-east-1
      access-key-id: ${S3_ACCESS_KEY}
      secret-access-key: ${S3_SECRET_KEY}

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

- **[Docker](../installation/docker.md)** - Quickest setup
- **[Docker Compose](../installation/docker-compose.md)** - Automated deployment
- **[NixOS](../installation/nixos.md)** - Native NixOS integration
- **[Kubernetes](../installation/kubernetes.md)** - For K8s environments
- **[Helm](../installation/helm.md)** - Simplified K8s deployment

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

```bash
# Check service is running
curl http://your-ncps:8501/nix-cache-info

# Get public key
curl http://your-ncps:8501/pubkey

# Check metrics (if enabled)
curl http://your-ncps:8501/metrics
```

### Configure Clients

See [Client Setup Guide](../usage/client-setup.md).

### Set Up Monitoring

See [Monitoring Guide](../operations/monitoring.md).

## Limitations

Single-instance deployments have these limitations:

- **No redundancy**: Server downtime = cache downtime
- **No load distribution**: One server handles all requests
- **No zero-downtime updates**: Updates require service restart
- **Limited scalability**: Cannot add more instances

**When you outgrow single-instance:** See [High Availability Guide](high-availability.md) for migration.

## Troubleshooting

### Service Won't Start

```bash
# Check logs
journalctl -u ncps -f  # systemd
docker logs ncps       # Docker

# Common issues:
# - Missing required flags (hostname, storage, upstream)
# - Database not initialized
# - Permission errors
```

### Performance Issues

```bash
# Check disk space
df -h

# Check memory usage
free -h

# Monitor cache hit rate in logs or metrics
```

### Storage Full

```bash
# Enable LRU cleanup
--cache-max-size=100G
--cache-lru-schedule="0 2 * * *"

# Manually clean up (DANGER: deletes cache)
rm -rf /var/lib/ncps/nar/*
```

See the [Troubleshooting Guide](../operations/troubleshooting.md) for more help.

## Next Steps

1. **[Configure Clients](../usage/client-setup.md)** - Set up Nix clients
2. **[Configure Monitoring](../operations/monitoring.md)** - Set up observability
3. **[Review Operations](../operations/)** - Learn about backups, upgrades, etc.
4. **Consider [HA Deployment](high-availability.md)** - When you need to scale

## Related Documentation

- [Installation Guides](../installation/) - Installation methods
- [Configuration Reference](../configuration/reference.md) - All configuration options
- [Storage Configuration](../configuration/storage.md) - Storage backend details
- [Database Configuration](../configuration/database.md) - Database backend details
- [High Availability](high-availability.md) - Migrate to HA when needed
