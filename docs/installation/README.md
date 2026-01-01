[Home](../../README.md) > [Documentation](../README.md) > Installation

# Installation Guide

Choose the installation method that best fits your environment and requirements.

## Installation Methods

| Method | Best For | Complexity | Documentation |
|--------|----------|------------|---------------|
| **[Docker](docker.md)** | Quick setup, testing, single-instance | ⭐ Easy | [Docker Guide](docker.md) |
| **[Docker Compose](docker-compose.md)** | Automated setup, reproducible deployments | ⭐⭐ Moderate | [Docker Compose Guide](docker-compose.md) |
| **[Kubernetes](kubernetes.md)** | Production, manual K8s control | ⭐⭐⭐ Advanced | [Kubernetes Guide](kubernetes.md) |
| **[Helm Chart](helm.md)** | Production K8s, simplified management | ⭐⭐ Moderate | [Helm Guide](helm.md) |
| **[NixOS](nixos.md)** | NixOS systems, native integration | ⭐⭐ Moderate | [NixOS Guide](nixos.md) |

## Quick Recommendations

### For Development/Testing
**Use [Docker](docker.md)**
- Fastest setup (5 minutes)
- No infrastructure required
- Perfect for trying ncps

### For Small Teams (1-10 users)
**Use [Docker Compose](docker-compose.md)** or **[NixOS](nixos.md)**
- Automated setup and management
- Easy to maintain
- Good for single-server deployments

### For Production (Single Instance)
**Use [Kubernetes](kubernetes.md)** or **[Helm Chart](helm.md)**
- Better resource management
- Built-in health checks and restarts
- Integration with existing infrastructure

### For Production (High Availability)
**Use [Helm Chart](helm.md)**
- Simplified multi-instance deployment
- Built-in HA configuration
- Handles Redis, load balancing, and more

## Prerequisites by Method

### Docker
- Docker installed (version 20.10+)
- 2GB+ available disk space
- Network access to Docker Hub

### Docker Compose
- Docker and Docker Compose installed
- 2GB+ available disk space

### Kubernetes
- Kubernetes cluster (version 1.20+)
- kubectl configured
- PersistentVolume provisioner
- 2GB+ available storage

### Helm
- Kubernetes cluster (version 1.20+)
- Helm installed (version 3.0+)
- kubectl configured

### NixOS
- NixOS 25.05 or later
- Sufficient disk space for cache

## Common Post-Installation Steps

After installing ncps with any method:

1. **Verify Installation**
   ```bash
   # Test cache info endpoint
   curl http://your-ncps-hostname:8501/nix-cache-info

   # Get public key
   curl http://your-ncps-hostname:8501/pubkey
   ```

2. **[Configure Clients](../usage/client-setup.md)**
   - Add ncps as a substituter
   - Add public key to trusted keys

3. **[Configure Monitoring](../operations/monitoring.md)** (Optional but recommended)
   - Enable Prometheus metrics
   - Set up alerts

## Comparison: Local vs S3 Storage

### Local Storage
**Pros:**
- Simple setup, no external dependencies
- Lower latency for single-instance
- No S3 costs

**Cons:**
- Not suitable for HA deployments
- Limited to single server's disk
- No built-in redundancy

### S3 Storage
**Pros:**
- Required for HA deployments
- Scalable and redundant
- Works with AWS S3, MinIO, and others

**Cons:**
- Requires S3 service setup
- Potential costs (AWS S3)
- Slight latency overhead

See [Storage Configuration](../configuration/storage.md) for details.

## Comparison: SQLite vs PostgreSQL/MySQL

### SQLite
**Pros:**
- Embedded, no external service
- Zero configuration
- Perfect for single-instance

**Cons:**
- NOT supported for HA
- Single connection limit
- File-based limitations

### PostgreSQL/MySQL
**Pros:**
- Required for HA deployments
- Handles concurrent connections
- Production-ready scaling

**Cons:**
- Requires database service
- More complex setup
- Additional maintenance

See [Database Configuration](../configuration/database.md) for details.

## Need Help?

- [Troubleshooting Guide](../operations/troubleshooting.md)
- [Configuration Reference](../configuration/reference.md)
- [GitHub Discussions](https://github.com/kalbasit/ncps/discussions)

## Next Steps

After installation:
1. [Configure ncps](../configuration/reference.md)
2. [Set up Nix clients](../usage/client-setup.md)
3. [Monitor your cache](../operations/monitoring.md)
