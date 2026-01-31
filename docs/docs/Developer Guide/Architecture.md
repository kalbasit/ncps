# Architecture
## Architecture Documentation

Deep dive into ncps internals and design.

## Guides

*   <a class="reference-link" href="Architecture/Components.md">Components</a> - System components and their interactions
*   <a class="reference-link" href="Architecture/Storage%20Backends.md">Storage Backends</a> - Local and S3 storage implementation
*   <a class="reference-link" href="Architecture/Request%20Flow.md">Request Flow</a> - Detailed request processing flow

## Overview

ncps is designed as a modular caching proxy with pluggable storage and database backends.

## Key Design Principles

1.  **Modularity** - Separate concerns (storage, database, locks, server)
2.  **Flexibility** - Support multiple backends for storage and database
3.  **Scalability** - Scale from single instance to high availability
4.  **Simplicity** - Easy to deploy and operate

## System Architecture

```
┌─────────────────────────────────────┐
│           HTTP Server (Chi)         │
└───────────────┬─────────────────────┘
                │
┌───────────────▼─────────────────────┐
│         Cache Layer                 │
│  - Request handling                 │
│  - Upstream fetching                │
│  - Signing                          │
└───┬─────────────┬───────────────────┘
    │             │
    ▼             ▼
┌────────┐    ┌────────────┐
│Storage │    │  Database  │
│Backend │    │  Backend   │
└────────┘    └────────────┘
```

## Related Documentation

*   <a class="reference-link" href="Architecture/Components.md">Components</a> - Detailed component breakdown
*   <a class="reference-link" href="Architecture/Storage%20Backends.md">Storage Backends</a> - Storage implementation
*   <a class="reference-link" href="Architecture/Request%20Flow.md">Request Flow</a> - Request processing
*   <a class="reference-link" href="../Developer%20Guide.md">Developer Guide</a> - Contributing