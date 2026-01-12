# User Guide

## ncps Documentation

Welcome to the comprehensive documentation for ncps (Nix Cache Proxy Server). This documentation is organized to help you quickly find the information you need, whether you're just getting started or managing a production deployment.

## Quick Navigation

| Category | What's Inside | Start Here |
| --- | --- | --- |
| <a class="reference-link" href="User%20Guide/Getting%20Started.md">Getting Started</a> | Quick start guide, core concepts, architecture overview | <a class="reference-link" href="User%20Guide/Getting%20Started/Quick%20Start.md">Quick Start</a> |
| <a class="reference-link" href="User%20Guide/Installation.md">Installation</a> | Installation guides for all deployment methods | [Installation Methods](User%20Guide/Installation.md) |
| <a class="reference-link" href="User%20Guide/Configuration.md">Configuration</a> | Complete configuration reference and guides | [Configuration Reference](User%20Guide/Configuration/Reference.md) |
| <a class="reference-link" href="User%20Guide/Deployment.md">Deployment</a> | Single-instance and high-availability deployment | [Deployment Modes](User%20Guide/Deployment.md) |
| <a class="reference-link" href="User%20Guide/Usage.md">Usage</a> | Client setup and cache management | <a class="reference-link" href="User%20Guide/Usage/Client%20Setup.md">Client Setup</a> |
| <a class="reference-link" href="User%20Guide/Operations.md">Operations</a> | Monitoring, troubleshooting, backup, upgrades | <a class="reference-link" href="User%20Guide/Operations/Monitoring.md">Monitoring</a> |
| <a class="reference-link" href="Developer%20Guide/Architecture.md">Architecture</a> | System architecture and design details | <a class="reference-link" href="Developer%20Guide/Architecture/Components.md">Components</a> |
| <a class="reference-link" href="Developer%20Guide.md">Developer Guide</a> | Contributing and development guides | <a class="reference-link" href="Developer%20Guide/Contributing.md">Contributing</a> |

## Documentation by Task

### I want to...

**Get Started**

- [Run ncps for the first time](User%20Guide/Getting%20Started/Quick%20Start.md)
- [Understand how ncps works](User%20Guide/Getting%20Started/Concepts.md)
- [Choose the right deployment method](User%20Guide/Installation.md)

**Install ncps**

- [Install with Docker](User%20Guide/Installation/Docker.md)
- [Install with Docker Compose](User%20Guide/Installation/Docker%20Compose.md)
- [Deploy on Kubernetes](User%20Guide/Installation/Kubernetes.md)
- [Use the Helm chart](User%20Guide/Installation/Helm%20Chart.md)
- [Configure on NixOS](User%20Guide/Installation/NixOS.md)

**Configure ncps**

- [See all configuration options](User%20Guide/Configuration/Reference.md)
- [Configure storage backends](User%20Guide/Configuration/Storage.md)
- [Choose and configure a database](User%20Guide/Configuration/Database.md)
- [Configure analytics reporting](User%20Guide/Configuration/Analytics.md)
- [Set up observability](User%20Guide/Configuration/Observability.md)

**Deploy for Production**

- [Deploy a single instance](User%20Guide/Deployment/Single%20Instance.md)
- [Set up high availability](User%20Guide/Deployment/High%20Availability.md)
- [Configure distributed locking](User%20Guide/Deployment/Distributed%20Locking.md)

**Use ncps**

- [Configure Nix clients](User%20Guide/Usage/Client%20Setup.md)
- [Manage the cache](User%20Guide/Usage/Cache%20Management.md)

**Operate and Maintain**

- [Monitor ncps](User%20Guide/Operations/Monitoring.md)
- [Troubleshoot issues](User%20Guide/Operations/Troubleshooting.md)
- [Backup and restore](User%20Guide/Operations/Backup%20Restore.md)
- [Upgrade ncps](User%20Guide/Operations/Upgrading.md)

**Understand Internals**

- [Learn about system components](Developer%20Guide/Architecture/Components.md)
- [Understand storage backends](Developer%20Guide/Architecture/Storage%20Backends.md)
- [Helm Chart Documentation](User%20Guide/Installation/Helm%20Chart/Chart%20Reference.md) - Comprehensive Helm chart reference
- [Follow request flow](Developer%20Guide/Architecture/Request%20Flow.md)
- [Set up development environment](Developer%20Guide.md)
- [See contribution guidelines](Developer%20Guide/Contributing.md)

## Additional Resources

- [Helm Chart Documentation](User%20Guide/Installation/Helm%20Chart/Chart%20Reference.md) - Comprehensive Helm chart reference
- [See contribution guidelines](Developer%20Guide/Contributing.md) - Development and contribution guidelines
- [Configuration Example](https://github.com/kalbasit/ncps/blob/main/config.example.yaml) - Complete configuration file example
- [**GitHub Issues**](https://github.com/kalbasit/ncps/issues) - Report bugs and request features
- [**Discussions**](https://github.com/kalbasit/ncps/discussions) - Ask questions and share ideas

## Documentation Versions

This documentation is for the latest version of ncps. For version-specific documentation, please refer to the corresponding Git tag or release branch.

## Contributing to Documentation

Found an error or want to improve the documentation? Contributions are welcome!

1. Documentation source files are in the `/docs` directory
1. Submit pull requests with improvements
1. Follow the existing structure and style
1. See <a class="reference-link" href="Developer%20Guide/Contributing.md">Contributing</a> for guidelines

______________________________________________________________________

**Need Help?** Check the <a class="reference-link" href="User%20Guide/Operations/Troubleshooting.md">Troubleshooting</a> or ask in [Discussions](https://github.com/kalbasit/ncps/discussions).
