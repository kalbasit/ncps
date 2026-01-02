[Home](../../README.md) > [Documentation](../README.md) > Getting Started

# Getting Started with ncps

This section will help you get up and running with ncps quickly and understand the core concepts.

## Quick Links

- **[Quick Start](quick-start.md)** - Get ncps running in minutes with Docker
- **[Core Concepts](concepts.md)** - Understand how ncps works and why to use it

## What You'll Learn

### Quick Start

Learn how to:

- Install ncps with Docker in minutes
- Run with local storage (simplest setup)
- Run with S3 storage (scalable setup)
- Verify your installation works
- Get the public key for client configuration

### Core Concepts

Understand:

- What ncps is and the problems it solves
- How the request flow works
- Storage architecture (local vs S3)
- Database options (SQLite, PostgreSQL, MySQL)
- When to use single-instance vs high-availability mode

## Next Steps

After completing the getting started guides:

1. **Installation** - Choose your [installation method](../installation/)
1. **Configuration** - Review the [configuration options](../configuration/reference.md)
1. **Client Setup** - Configure your [Nix clients](../usage/client-setup.md)
1. **Deployment** - Plan your [deployment strategy](../deployment/)

## Common Questions

**Do I need Redis?**

- No, not for single-instance deployments
- Yes, for high-availability with multiple instances

**Should I use S3 or local storage?**

- Local storage: Simple, single-instance deployments
- S3 storage: Multi-instance HA deployments or cloud-native setups

**Which database should I use?**

- SQLite: Simple, single-instance deployments
- PostgreSQL/MySQL: Multi-instance HA deployments

See [Core Concepts](concepts.md) for detailed explanations.
