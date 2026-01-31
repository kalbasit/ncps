# Installation
## Installation Guide

Choose the installation method that best fits your environment and requirements.

## Installation Methods

| Method | Best For | Complexity | Documentation |
| --- | --- | --- | --- |
| <a class="reference-link" href="Installation/Docker.md">Docker</a> | Quick setup, testing, single-instance | ⭐ Easy | [Docker Guide](Installation/Docker.md) |
| <a class="reference-link" href="Installation/Docker%20Compose.md">Docker Compose</a> | Automated setup, reproducible deployments | ⭐⭐ Moderate | [Docker Compose Guide](Installation/Docker%20Compose.md) |
| <a class="reference-link" href="Installation/Kubernetes.md">Kubernetes</a> | Production, manual K8s control | ⭐⭐⭐ Advanced | [Kubernetes Guide](Installation/Kubernetes.md) |
| <a class="reference-link" href="Installation/Helm%20Chart.md">Helm Chart</a> | Production K8s, simplified management | ⭐⭐ Moderate | [Helm Guide](Installation/Helm%20Chart.md) |
| <a class="reference-link" href="Installation/NixOS.md">NixOS</a> | NixOS systems, native integration | ⭐⭐ Moderate | [NixOS Guide](Installation/NixOS.md) |

## Quick Recommendations

### For Development/Testing

**Use** <a class="reference-link" href="Installation/Docker.md">Docker</a>

*   Fastest setup (5 minutes)
*   No infrastructure required
*   Perfect for trying ncps

### For Small Teams (1-10 users)

**Use** <a class="reference-link" href="Installation/Docker%20Compose.md">Docker Compose</a> or <a class="reference-link" href="Installation/NixOS.md">NixOS</a>

*   Automated setup and management
*   Easy to maintain
*   Good for single-server deployments

### For Production (Single Instance)

**Use** <a class="reference-link" href="Installation/Kubernetes.md">Kubernetes</a> or <a class="reference-link" href="Installation/Helm%20Chart.md">Helm Chart</a>

*   Better resource management
*   Built-in health checks and restarts
*   Integration with existing infrastructure

### For Production (High Availability)

**Use** <a class="reference-link" href="Installation/Helm%20Chart.md">Helm Chart</a>

*   Simplified multi-instance deployment
*   Built-in HA configuration
*   Handles Redis, load balancing, and more

## Prerequisites by Method

### Docker

*   Docker installed (version 20.10+)
*   2GB+ available disk space
*   Network access to a container registry (GHCR or Docker Hub)

### Docker Compose

*   Docker and Docker Compose installed
*   2GB+ available disk space

### Kubernetes

*   Kubernetes cluster (version 1.20+)
*   kubectl configured
*   PersistentVolume provisioner
*   2GB+ available storage

### Helm

*   Kubernetes cluster (version 1.20+)
*   Helm installed (version 3.0+)
*   kubectl configured

### NixOS

*   NixOS 25.05 or later
*   Sufficient disk space for cache

## Common Post-Installation Steps

After installing ncps with any method:

1.  **Verify Installation**

    ```
    # Test cache info endpoint
    curl http://your-ncps-hostname:8501/nix-cache-info

    # Get public key
    curl http://your-ncps-hostname:8501/pubkey
    ```
2.  <a class="reference-link" href="Usage/Client%20Setup.md">Client Setup</a>

    *   Add ncps as a substituter
    *   Add public key to trusted keys
3.  <a class="reference-link" href="Operations/Monitoring.md">Monitoring</a> (Optional but recommended)

    *   Enable Prometheus metrics
    *   Set up alerts

## Comparison: Local vs S3 Storage

### Local Storage

**Pros:**

*   Simple setup, no external dependencies
*   Lower latency for single-instance
*   No S3 costs

**Cons:**

*   Not suitable for HA deployments
*   Limited to single server's disk
*   No built-in redundancy

### S3 Storage

**Pros:**

*   Required for HA deployments
*   Scalable and redundant
*   Works with AWS S3, MinIO, and others

**Cons:**

*   Requires S3 service setup
*   Potential costs (AWS S3)
*   Slight latency overhead

See <a class="reference-link" href="Configuration/Storage.md">Storage</a> for details.

## Comparison: SQLite vs PostgreSQL/MySQL

### SQLite

**Pros:**

*   Embedded, no external service
*   Zero configuration
*   Perfect for single-instance

**Cons:**

*   NOT supported for HA
*   Single connection limit
*   File-based limitations

### PostgreSQL/MySQL

**Pros:**

*   Required for HA deployments
*   Handles concurrent connections
*   Production-ready scaling

**Cons:**

*   Requires database service
*   More complex setup
*   Additional maintenance

See <a class="reference-link" href="Configuration/Database.md">Database</a> for details.

## Need Help?

*   <a class="reference-link" href="Operations/Troubleshooting.md">Troubleshooting</a>
*   [Configuration Reference](Configuration/Reference.md)
*   [GitHub Discussions](https://github.com/kalbasit/ncps/discussions)

## Next Steps

After installation:

1.  [Configure ncps](Configuration/Reference.md)
2.  [Set up Nix clients](Usage/Client%20Setup.md)
3.  [Monitor your cache](Operations/Monitoring.md)