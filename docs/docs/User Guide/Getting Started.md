# Getting Started
## Getting Started with ncps

This section will help you get up and running with ncps quickly and understand the core concepts.

## Quick Links

*   <a class="reference-link" href="Getting%20Started/Quick%20Start.md">Quick Start</a> - Get ncps running in minutes with Docker
*   <a class="reference-link" href="Getting%20Started/Concepts.md">Concepts</a> - Understand how ncps works and why to use it

## What You'll Learn

### Quick Start

Learn how to:

*   Install ncps with Docker in minutes
*   Run with local storage (simplest setup)
*   Run with S3 storage (scalable setup)
*   Verify your installation works
*   Get the public key for client configuration

### Core Concepts

Understand:

*   What ncps is and the problems it solves
*   How the request flow works
*   Storage architecture (local vs S3)
*   Database options (SQLite, PostgreSQL, MySQL)
*   When to use single-instance vs high-availability mode

## Next Steps

After completing the getting started guides:

1.  **Installation** - Choose your [installation method](Installation.md)
2.  **Configuration** - Review the [configuration options](Configuration/Reference.md)
3.  **Client Setup** - Configure your [Nix clients](Usage/Client%20Setup.md)
4.  **Deployment** - Plan your [deployment strategy](Deployment.md)

## Common Questions

**Do I need Redis?**

*   No, not for single-instance deployments
*   Yes, for high-availability with multiple instances

**Should I use S3 or local storage?**

*   Local storage: Simple, single-instance deployments
*   S3 storage: Multi-instance HA deployments or cloud-native setups

**Which database should I use?**

*   SQLite: Simple, single-instance deployments
*   PostgreSQL/MySQL: Multi-instance HA deployments

See <a class="reference-link" href="Getting%20Started/Concepts.md">Concepts</a> for detailed explanations.