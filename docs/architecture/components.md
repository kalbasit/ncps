[Home](../../README.md) > [Documentation](../README.md) > [Architecture](README.md) > Components

# Components

Detailed breakdown of ncps system components.

## HTTP Server (pkg/server/)

**Technology:** Chi router

**Responsibilities:**

- Handle HTTP requests
- Route requests to handlers
- Serve static files (pubkey, nix-cache-info)
- Middleware (logging, metrics, tracing)

**Key Endpoints:**

- `GET /pubkey` - Public key
- `GET /nix-cache-info` - Cache metadata
- `GET /<hash>.narinfo` - Package metadata
- `GET /nar/<path>` - Package archive
- `GET /metrics` - Prometheus metrics (if enabled)

## Cache Layer (pkg/cache/)

**Responsibilities:**

- Check if package exists in cache
- Fetch from upstream if not cached
- Sign NarInfo files
- Coordinate downloads (HA mode)

**Key Functions:**

- `GetNarInfo()` - Get package metadata
- `GetNar()` - Get package archive
- `DownloadAndCache()` - Fetch from upstream

## Storage Backends (pkg/storage/)

**Interface:** `ConfigStore`, `NarInfoStore`, `NarStore`

**Implementations:**

- **Local** (`storage/local/`) - Filesystem storage
- **S3** (`storage/s3/`) - S3-compatible storage

**Responsibilities:**

- Store and retrieve NAR files
- Store and retrieve NarInfo files
- Store secret keys

## Database Backends (pkg/database/)

**Technology:** sqlc for type-safe SQL

**Implementations:**

- **SQLite** (`database/sqlitedb/`)
- **PostgreSQL** (`database/postgresdb/`)
- **MySQL** (`database/mysqldb/`)

**Schema:** `db/schema.sql`
**Queries:** `db/query.*.sql`

**Responsibilities:**

- Store NarInfo metadata
- Track cache size
- Store download state

## Lock Manager (pkg/lock/)

**Implementations:**

- **Local** (`lock/local/`) - In-process locks (sync.Mutex)
- **Redis** (`lock/redis/`) - Distributed locks (Redlock)

**Responsibilities:**

- Coordinate downloads (prevent duplicates)
- Coordinate LRU cleanup
- Handle lock retries

## NAR Handler (pkg/nar/)

**Responsibilities:**

- Parse NAR format
- Handle compression (xz, zstd)
- Extract metadata

## Component Interaction

```
HTTP Request
    ↓
Server (Chi)
    ↓
Cache Layer
    ↓
Lock Manager → Storage Backend → Database Backend
    ↓
Response
```

## Related Documentation

- [Storage Backends](storage-backends.md) - Storage details
- [Request Flow](request-flow.md) - Request processing
- [Development Guide](../development/) - Code structure
