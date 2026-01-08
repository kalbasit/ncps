[Home](../README.md) > Documentation

# ncps Documentation

Welcome to the comprehensive documentation for ncps (Nix Cache Proxy Server). This documentation is organized to help you quickly find the information you need, whether you're just getting started or managing a production deployment.

## Quick Navigation

| Category | What's Inside | Start Here |
|----------|---------------|------------|
| **[Getting Started](getting-started/)** | Quick start guide, core concepts, architecture overview | [Quick Start](getting-started/quick-start.md) |
| **[Installation](installation/)** | Installation guides for all deployment methods | [Installation Methods](installation/) |
| **[Configuration](configuration/)** | Complete configuration reference and guides | [Configuration Reference](configuration/reference.md) |
| **[Deployment](deployment/)** | Single-instance and high-availability deployment | [Deployment Modes](deployment/) |
| **[Usage](usage/)** | Client setup and cache management | [Client Setup](usage/client-setup.md) |
| **[Operations](operations/)** | Monitoring, troubleshooting, backup, upgrades | [Monitoring](operations/monitoring.md) |
| **[Architecture](architecture/)** | System architecture and design details | [Components](architecture/components.md) |
| **[Development](development/)** | Contributing and development guides | [Development Guide](development/) |

## Documentation by Task

### I want to...

**Get Started**

- [Run ncps for the first time](getting-started/quick-start.md)
- [Understand how ncps works](getting-started/concepts.md)
- [Choose the right deployment method](installation/)

**Install ncps**

- [Install with Docker](installation/docker.md)
- [Install with Docker Compose](installation/docker-compose.md)
- [Deploy on Kubernetes](installation/kubernetes.md)
- [Use the Helm chart](installation/helm.md)
- [Configure on NixOS](installation/nixos.md)

**Configure ncps**

- [See all configuration options](configuration/reference.md)
- [Configure storage backends](configuration/storage.md)
- [Choose and configure a database](configuration/database.md)
- [Configure analytics reporting](configuration/analytics.md)
- [Set up observability](configuration/observability.md)

**Deploy for Production**

- [Deploy a single instance](deployment/single-instance.md)
- [Set up high availability](deployment/high-availability.md)
- [Configure distributed locking](deployment/distributed-locking.md)

**Use ncps**

- [Configure Nix clients](usage/client-setup.md)
- [Manage the cache](usage/cache-management.md)

**Operate and Maintain**

- [Monitor ncps](operations/monitoring.md)
- [Troubleshoot issues](operations/troubleshooting.md)
- [Backup and restore](operations/backup-restore.md)
- [Upgrade ncps](operations/upgrading.md)

**Understand Internals**

- [Learn about system components](architecture/components.md)

- [Understand storage backends](architecture/storage-backends.md)

- [Follow request flow](architecture/request-flow.md)

- **[Helm Chart Documentation](../charts/ncps/README.md)** - Comprehensive Helm chart reference

- [Set up development environment](development/)

- [Run tests](development/testing.md)

- [See contribution guidelines](../CONTRIBUTING.md)

## Additional Resources

- **[Helm Chart Documentation](/charts/ncps/README.md)** - Comprehensive Helm chart reference
- **[Contributing Guide](../CONTRIBUTING.md)** - Development and contribution guidelines
- **[Configuration Example](../config.example.yaml)** - Complete configuration file example
- **[GitHub Issues](https://github.com/kalbasit/ncps/issues)** - Report bugs and request features
- **[Discussions](https://github.com/kalbasit/ncps/discussions)** - Ask questions and share ideas

## Documentation Versions

This documentation is for the latest version of ncps. For version-specific documentation, please refer to the corresponding Git tag or release branch.

## Contributing to Documentation

Found an error or want to improve the documentation? Contributions are welcome!

1. Documentation source files are in the `/docs` directory
1. Submit pull requests with improvements
1. Follow the existing structure and style
1. See [CONTRIBUTING.md](../CONTRIBUTING.md) for guidelines

______________________________________________________________________

**Need Help?** Check the [Troubleshooting Guide](operations/troubleshooting.md) or ask in [Discussions](https://github.com/kalbasit/ncps/discussions).
