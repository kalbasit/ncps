<p align="center">
  <img src="https://github.com/kalbasit/ncps/raw/main/assets/images/logo.svg" alt="ncps logo" width="200"/>
</p>

# ncps: Nix Cache Proxy Server

> A high-performance proxy server that accelerates Nix dependency retrieval across your local network

[![Go Report Card](https://goreportcard.com/badge/github.com/kalbasit/ncps)](https://goreportcard.com/report/github.com/kalbasit/ncps)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Sponsor](https://img.shields.io/static/v1?label=Sponsor&message=%E2%9D%A4&logo=GitHub&color=%23fe8e86)](https://github.com/sponsors/kalbasit)

## What is ncps?

ncps acts as a local binary cache for Nix, fetching store paths from upstream caches (like cache.nixos.org) and storing them locally. This reduces download times and bandwidth usage, especially beneficial when multiple machines share the same dependencies.

## Key Features

- **Multi-upstream cache** with automatic failover
- **Flexible storage**: local filesystem or S3-compatible (AWS S3, MinIO, etc.)
- **Database support**: SQLite, PostgreSQL, or MySQL/MariaDB
- **High availability** with Redis distributed locking for zero-downtime deployments
- **Smart caching**: LRU management with configurable size limits
- **Secure signing**: Signs cached paths with private keys for integrity
- **Observability**: OpenTelemetry and Prometheus metrics support
- **Easy setup**: Simple configuration and deployment

## Quick Start

Get ncps running in minutes with Docker:

```bash
# Pull images and create storage
docker pull alpine && docker pull ghcr.io/kalbasit/ncps
docker volume create ncps-storage
docker run --rm -v ncps-storage:/storage alpine /bin/sh -c \
  "mkdir -m 0755 -p /storage/var && mkdir -m 0700 -p /storage/var/ncps && mkdir -m 0700 -p /storage/var/ncps/db"

# Initialize database
docker run --rm -v ncps-storage:/storage ghcr.io/kalbasit/ncps \
  /bin/dbmate --url=sqlite:/storage/var/ncps/db/db.sqlite up

# Start the server
docker run -d --name ncps -p 8501:8501 -v ncps-storage:/storage ghcr.io/kalbasit/ncps \
  /bin/ncps serve \
  --cache-hostname=your-ncps-hostname \
  --cache-storage-local=/storage \
  --cache-database-url=sqlite:/storage/var/ncps/db/db.sqlite \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

Your cache will be available at `http://localhost:8501`. See the [Quick Start Guide](https://docs.ncps.dev/user-guide/getting-started/quick-start.html) for more options including S3 storage.

## Documentation

- **[Getting Started](https://docs.ncps.dev/user-guide/getting-started.html)** - Quick start guide, core concepts, and architecture
- **[Installation](https://docs.ncps.dev/user-guide/installation.html)** - Docker, Docker Compose, Kubernetes, Helm, NixOS
- **[Configuration](https://docs.ncps.dev/user-guide/configuration.html)** - Complete configuration reference, storage and database options
- **[Deployment](https://docs.ncps.dev/user-guide/deployment.html)** - Single-instance and high-availability deployment guides
- **[Usage](https://docs.ncps.dev/user-guide/usage.html)** - Client setup and cache management
- **[Operations](https://docs.ncps.dev/user-guide/operations.html)** - Monitoring, troubleshooting, backup and upgrades
- **[Architecture](https://docs.ncps.dev/developer-guide/architecture.html)** - System architecture and design details
- [**Development**](https://docs.ncps.dev/developer-guide.html) - Contributing, development setup, and testing

## Installation Methods

| Method | Best For | Documentation |
| ------------------ | ------------------------------------- | ----------------------------------------------------------- |
| **Docker** | Quick setup, single-instance | [Docker Guide](https://docs.ncps.dev/user-guide/installation/docker.html) |
| **Docker Compose** | Automated setup with dependencies | [Docker Compose Guide](https://docs.ncps.dev/user-guide/installation/docker-compose.html) |
| **Kubernetes** | Production, manual K8s deployment | [Kubernetes Guide](https://docs.ncps.dev/user-guide/installation/kubernetes.html) |
| **Helm Chart** | Production, simplified K8s management | [Helm Guide](https://docs.ncps.dev/user-guide/installation/helm-chart.html) |
| **NixOS** | NixOS systems with native integration | [NixOS Guide](https://docs.ncps.dev/user-guide/installation/nixos.html) |

## Deployment Modes

- **Single-instance**: Simple deployment with local or S3 storage, SQLite or shared database
- **High Availability**: Multiple instances with S3 storage, PostgreSQL/MySQL, and Redis for zero-downtime operation

See the [Deployment Guide](https://docs.ncps.dev/user-guide/deployment.html) for detailed setup instructions.

## Support the Project

If you find `ncps` useful, please consider supporting its development! Sponsoring helps maintain the project, fund new features, and ensure long-term sustainability.

[![Sponsor this project](https://img.shields.io/static/v1?label=Sponsor&message=%E2%9D%A4&logo=GitHub&color=%23fe8e86)](https://github.com/sponsors/kalbasit)

## Contributing

Contributions are welcome! We appreciate bug reports, feature requests, documentation improvements, and code contributions.

See the [Developer Guide](https://docs.ncps.dev/developer-guide.html) for:

- Development setup and workflow
- Code quality standards and testing procedures
- How to submit pull requests

## License

This project is licensed under the **MIT License** - see the [LICENSE](LICENSE) file for details.

______________________________________________________________________

[Report Bug](https://github.com/kalbasit/ncps/issues) • [Request Feature](https://github.com/kalbasit/ncps/issues) • [Discussions](https://github.com/kalbasit/ncps/discussions) • [Sponsor](https://github.com/sponsors/kalbasit)
