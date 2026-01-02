[Home](../../README.md) > [Documentation](../README.md) > Deployment

# Deployment Guide

Choose the deployment strategy that best fits your requirements.

## Deployment Modes

ncps supports two primary deployment modes:

### Single-Instance Deployment

One ncps server handling all cache requests.

**Best for:**

- Development and testing
- Small to medium teams (1-100+ users)
- Single-location deployments
- Simpler operations

**Characteristics:**

- One server
- Local locks (no Redis)
- Local or S3 storage
- SQLite, PostgreSQL, or MySQL database
- Easier to set up and maintain

[Learn more →](single-instance.md)

### High Availability Deployment

Multiple ncps instances for redundancy and scalability.

**Best for:**

- Production environments
- Large teams (100+ users)
- Business-critical infrastructure
- Geographic distribution
- Zero-downtime requirements

**Characteristics:**

- 2+ servers
- Redis distributed locking
- S3 storage (required)
- PostgreSQL or MySQL database (required, NOT SQLite)
- Load balancer
- More complex setup

[Learn more →](high-availability.md)

## Quick Comparison

| Aspect | Single-Instance | High Availability |
|--------|-----------------|-------------------|
| **Servers** | 1 | 2+ |
| **Locking** | Local (in-process) | Redis distributed locks |
| **Storage** | Local or S3 | S3 (required) |
| **Database** | SQLite, PostgreSQL, or MySQL | PostgreSQL or MySQL (NOT SQLite) |
| **Load Balancer** | Not needed | Required |
| **Redundancy** | None | Full |
| **Complexity** | Simple | Moderate |
| **Zero Downtime** | No | Yes |
| **Scalability** | Limited to one server | Horizontal scaling |
| **Cost** | Lower | Higher |

## Decision Tree

```
Start
  │
  ├─ Need zero-downtime updates? ──Yes──> High Availability
  ├─ Need geographic distribution? ──Yes──> High Availability
  ├─ Team > 100 users? ──Yes──> High Availability
  ├─ Mission-critical service? ──Yes──> High Availability
  │
  └─ Otherwise ──> Single-Instance
```

## Documentation

- **[Single-Instance Deployment](single-instance.md)** - Deploy one ncps server
- **[High Availability Deployment](high-availability.md)** - Deploy multiple instances with HA
- **[Distributed Locking](distributed-locking.md)** - Deep dive into Redis locking for HA

## Prerequisites by Mode

### Single-Instance Prerequisites

**Minimum:**

- Server or VM (2+ CPU cores, 4GB+ RAM recommended)
- Storage (50GB-1TB depending on usage)
- Network connectivity to upstream caches

**Optional:**

- S3-compatible storage (for cloud-native or future HA)
- PostgreSQL/MySQL (for better performance than SQLite)

### High Availability Prerequisites

**Required:**

- 2+ servers (3+ recommended for better availability)
- Redis server (single instance or cluster)
- S3-compatible storage (AWS S3, MinIO, etc.)
- PostgreSQL or MySQL database
- Load balancer (nginx, HAProxy, cloud LB)

**Optional:**

- Monitoring and alerting (Prometheus, Grafana)
- Centralized logging (ELK, Loki)

## Getting Started

1. **Choose deployment mode** based on your requirements
1. **Review prerequisites** for your chosen mode
1. **Follow installation guide**:
   - [Docker](../installation/docker.md)
   - [Docker Compose](../installation/docker-compose.md)
   - [Kubernetes](../installation/kubernetes.md)
   - [Helm Chart](../installation/helm.md)
   - [NixOS](../installation/nixos.md)
1. **Configure** according to your mode:
   - [Single-Instance Configuration](single-instance.md#configuration)
   - [HA Configuration](high-availability.md#configuration)
1. **Verify deployment** and test
1. **Set up monitoring** (recommended)

## Migration Path

### From Single-Instance to HA

Common migration path as your needs grow:

1. **Start**: Single instance with local storage and SQLite
1. **Scale up**: Move to PostgreSQL for better performance
1. **Cloud-ready**: Migrate to S3 storage
1. **High Availability**: Add Redis and additional instances

Each step is incremental and can be done independently.

See [High Availability Guide](high-availability.md#migration-from-single-instance) for detailed migration steps.

## Common Deployment Patterns

### Pattern 1: Development Setup

```
Developer Workstation
└── ncps (Docker)
    ├── Local storage
    └── SQLite
```

### Pattern 2: Small Team

```
Shared Server
└── ncps (systemd)
    ├── Local NFS storage
    └── SQLite or PostgreSQL
```

### Pattern 3: Cloud Production (Single)

```
Cloud VM
└── ncps (Docker/Kubernetes)
    ├── S3 storage
    └── Managed PostgreSQL (RDS, etc.)
```

### Pattern 4: High Availability

```
Load Balancer
├── ncps Instance 1
├── ncps Instance 2
└── ncps Instance 3
    ├── Shared S3 storage
    ├── Shared PostgreSQL
    └── Shared Redis
```

## Next Steps

1. **[Choose and follow deployment guide](single-instance.md)**
1. **[Configure clients](../usage/client-setup.md)** to use your cache
1. **[Set up monitoring](../operations/monitoring.md)** for production
1. **[Review operations guides](../operations/)** for maintenance

## Related Documentation

- [Installation Guides](../installation/) - Installation methods
- [Configuration Reference](../configuration/reference.md) - All configuration options
- [Operations Guides](../operations/) - Monitoring, troubleshooting, backups
