# Database
## Database Configuration

Configure ncps database backends: SQLite, PostgreSQL, or MySQL/MariaDB.

## Overview

ncps supports three database backends for storing metadata (NarInfo, cache statistics, etc.):

*   **SQLite**: Embedded database, no external dependencies
*   **PostgreSQL**: Production-ready, concurrent access support
*   **MySQL/MariaDB**: Production-ready, concurrent access support

## Quick Comparison

| Feature | SQLite | PostgreSQL | MySQL/MariaDB |
| --- | --- | --- | --- |
| **Setup Complexity** | None (embedded) | Moderate | Moderate |
| **External Service** | ❌ No | ✅ Yes | ✅ Yes |
| **Concurrent Writes** | ❌ Limited (1 connection) | ✅ Excellent | ✅ Excellent |
| **HA Support** | ❌ Not supported | ✅ Supported | ✅ Supported |
| **Performance** | Good (embedded) | Excellent | Excellent |
| **Best For** | Single-instance | HA, Production | HA, Production |

## Next Steps

1.  [Storage Configuration](Storage.md) - Configure storage backend
2.  [Configuration Reference](Reference.md) - All database options
3.  <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a> - PostgreSQL/MySQL for HA
4.  [Operations Guide](../Operations/Backup%20Restore.md) - Backup strategies

## Related Documentation

*   [Configuration Reference](Reference.md) - All configuration options
*   [High Availability Guide](../Deployment/High%20Availability.md) - HA database setup
*   [Backup and Restore Guide](../Operations/Backup%20Restore.md) - Backup strategies