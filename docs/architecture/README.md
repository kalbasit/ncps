[Home](../../README.md) > [Documentation](../README.md) > Architecture

# Architecture Documentation

Deep dive into ncps internals and design.

## Guides

- **[Components](components.md)** - System components and their interactions
- **[Storage Backends](storage-backends.md)** - Local and S3 storage implementation
- **[Request Flow](request-flow.md)** - Detailed request processing flow

## Overview

ncps is designed as a modular caching proxy with pluggable storage and database backends.

## Key Design Principles

1. **Modularity** - Separate concerns (storage, database, locks, server)
1. **Flexibility** - Support multiple backends for storage and database
1. **Scalability** - Scale from single instance to high availability
1. **Simplicity** - Easy to deploy and operate

## System Architecture

```
┌─────────────────────────────────────┐
│           HTTP Server (Chi)          │
└───────────────┬─────────────────────┘
                │
┌───────────────▼─────────────────────┐
│         Cache Layer                  │
│  - Request handling                  │
│  - Upstream fetching                 │
│  - Signing                           │
└───┬─────────────┬───────────────────┘
    │             │
    ▼             ▼
┌────────┐    ┌────────────┐
│Storage │    │  Database  │
│Backend │    │  Backend   │
└────────┘    └────────────┘
```

## Related Documentation

- [Components](components.md) - Detailed component breakdown
- [Storage Backends](storage-backends.md) - Storage implementation
- [Request Flow](request-flow.md) - Request processing
- [Development Guide](../development/) - Contributing
