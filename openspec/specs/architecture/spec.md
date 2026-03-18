# Architecture Specification

## Overview

ncps (Nix Cache Proxy Server) is a Go HTTP proxy that sits between Nix clients and upstream binary caches (e.g., `cache.nixos.org`). It intercepts `.narinfo` and `.nar` requests, caches artifacts locally, and re-serves them—reducing external bandwidth and download latency.

---

## Package Structure

```
cmd/                        CLI entrypoints (serve, migrate-narinfo, global flags, OTel bootstrap)
pkg/
  cache/                    Core cache logic: fetch, store, evict, CDC, LRU
    upstream/               Upstream cache HTTP client
    healthcheck/            Health-check helpers
  server/                   HTTP server (chi router), handler functions
  database/                 Database abstraction layer
    sqlitedb/               sqlc-generated SQLite querier + adapter
    postgresdb/             sqlc-generated PostgreSQL querier + adapter
    mysqldb/                sqlc-generated MySQL/MariaDB querier + adapter
  storage/
    local/                  Local filesystem NarInfoStore + NarStore
    s3/                     S3-compatible NarInfoStore + NarStore (MinIO)
    chunk/                  chunk.Store implementations (local + S3)
  nar/                      NAR URL parsing, hash validation, compression types
  narinfo/                  narinfo parsing helpers (re-exports go-nix library types)
  lock/
    local/                  In-process mutex-based Locker
    redis/                  Redis-backed distributed Locker (redsync)
  config/                   Secret key and runtime configuration
  chunker/                  CDC chunker (content-defined chunking algorithm)
  zstd/                     Pooled zstd reader/writer helpers
  analytics/                Analytics middleware types
db/
  migrations/{sqlite,postgres,mysql}/   dbmate migration files per engine
  query.{sqlite,postgres,mysql}.sql     sqlc query files per engine
  schema/{sqlite,postgres,mysql}.sql    Derived schema snapshots (via migrate-up)
```

---

## Request Lifecycle

### Standard Proxy Flow (CDC disabled)

```
Nix client
  │
  ▼
pkg/server  (chi router)
  │  HEAD/GET /{hash}.narinfo
  │  HEAD/GET /nar/{hash}.nar[.{compression}]
  │
  ▼
pkg/cache.Cache.GetNarInfo(ctx, hash)
  1. Check database (narinfos table) → cache HIT: return immediately
  2. Check storage backend (NarInfoStore.HasNarInfo) → legacy HIT: return + background DB migration
  3. Upstream fetch via upstream.Cache → cache MISS: pull, validate signature, store in DB + storage, return

pkg/cache.Cache.GetNar(ctx, narURL)
  1. Check storage backend (NarStore.HasNar) → HIT: stream from storage
  2. Check DB for CDC chunks (HasNarInChunks) → HIT: stream chunks progressively
  3. Upstream fetch → MISS: pull into temp file, move to storage, store DB record
```

### CDC Enabled, Lazy Disabled (synchronous chunking)

When CDC is enabled and `cdcLazyChunkingEnabled == false`:

1. NAR is downloaded from upstream into a temp file (via `pullNarIntoStore`).
2. `storeNarWithCDC` is called **synchronously** before returning to the client.
3. The NAR is split into content-defined chunks by `chunker.CDCChunker`.
4. Each chunk is zstd-compressed and written to `chunk.Store`.
5. Chunk records and their `nar_file_chunks` links are written to the database in progressive batches (first batch after 100ms, subsequent batches every 500ms, max 100 chunks/batch).
6. Once all chunks are recorded, the whole-file NAR is deleted from `narStore` after `cdcDeleteDelay`.
7. `narinfo.URL` is rewritten to `{hash}.nar` (no compression extension) to signal CDC storage.

### CDC Enabled, Lazy Enabled (background chunking)

When `cdcLazyChunkingEnabled == true`:

1. NAR is downloaded and stored as a whole file in `narStore` first.
2. The handler returns to the client immediately.
3. A background goroutine (limited by `cdcBackgroundWorkers`) calls `storeNarWithCDC` asynchronously.
4. Concurrent requests for the same NAR during chunking are streamed from the whole-file storage until chunking completes.
5. A distributed lock (`TryLock`) prevents thundering-herd duplicate chunking of the same NAR across multiple processes.

---

## Concurrency Model

| Concern | Mechanism |
|---|---|
| Narinfo download deduplication | `downloadState` per hash with `sync.Mutex` + `sync.Cond` broadcast |
| CDC background workers | `sync.WaitGroup` (`cdcWg`) + bounded goroutine pool (`cdcBackgroundWorkers`) |
| CDC chunk DB writes | Batched progressive commits (100ms / 500ms delays, 100-chunk cap) |
| Multi-process CDC deduplication | Distributed `TryLock` (Redis via redsync, or in-process mutex) |
| Stale CDC lock recovery | `chunking_started_at` timestamp; locks older than 1 hour are considered stale and cleaned up |
| SQLite write serialization | `MaxOpenConns=1`, WAL mode, `busy_timeout=10000ms` |
| PostgreSQL / MySQL pool | Configurable `MaxOpenConns` / `MaxIdleConns` via `database.PoolConfig` |
| OTel tracing | Every public cache and server method starts a child span |
| Metrics | OpenTelemetry `metric.Meter` for counters/histograms; Prometheus gatherer at `/metrics` |

### Memory Efficiency: Pooled zstd Readers/Writers

NAR files are large binary streams (often hundreds of MB). When a client requests a NAR with `Accept-Encoding: zstd` but the cached copy is stored uncompressed, the server must re-compress on the fly. Naively allocating a new `zstd.Writer` per request would cause severe GC pressure at high throughput — each encoder allocates large internal dictionaries and buffers.

To address this, `pkg/zstd` provides pooled `PooledReader` and `PooledWriter` types backed by `sync.Pool`. Encoders and decoders are reset and returned to the pool after each use rather than being garbage-collected. This dramatically reduces allocations-per-request and keeps GC pauses predictable under concurrent load. The same pooling applies when decompressing chunks in CDC streaming (`GetChunk` via `chunk.Store`).

---

## Database Architecture

ncps deliberately avoids ORMs. All database access goes through hand-written SQL queries stored in `db/query.{sqlite,postgres,mysql}.sql`. This keeps queries explicit, auditable, and engine-specific — we use the right SQL dialect per engine rather than a lowest-common-denominator abstraction.

**sqlc** reads those query files along with the migration-derived schema and generates three completely separate, engine-specific `Querier` interfaces and implementations:

- `pkg/database/sqlitedb` — SQLite-specific generated code; exploits SQLite's `RETURNING` clause and `ON CONFLICT` syntax.
- `pkg/database/postgresdb` — PostgreSQL-specific; uses `pgx/v5` driver, native `RETURNING`, and array types where applicable.
- `pkg/database/mysqldb` — MySQL/MariaDB-specific; handles `LAST_INSERT_ID()` and MySQL's lack of `RETURNING`.

The three packages are never mixed at runtime. `database.Open()` inspects the URL scheme and returns the matching engine wrapper as the shared `database.Querier` interface used by `pkg/cache`. This guarantees that engine-specific features (e.g., `RETURNING`, `ON CONFLICT`, transaction isolation) are used correctly per engine, not papered over.

**dbmate** manages schema migrations. The `dbmate` binary in dev and Docker is actually a thin Go wrapper (`nix/dbmate-wrapper/`) that reads the `--url` flag, auto-selects the migrations directory (`db/migrations/{sqlite,postgres,mysql}`) and schema output path (`db/schema/{engine}.sql`) from the URL scheme, and then delegates to the real `dbmate` binary. This means developers never need to specify `--migrations-dir` manually, and the same `dbmate` command works correctly across all three engines.

---

## LRU Eviction

- Controlled by `Cache.SetMaxSize(bytes)`.
- Scheduled via `Cache.AddLRUCronJob(ctx, schedule)` using an internal cron runner.
- `runLRU` queries `GetLeastUsedNarInfos` from the database (ordered by `last_accessed_at`).
- For each evicted `nar_file`: deletes chunks (CDC) or whole file (non-CDC) from storage, removes DB records.
- `narinfos` orphaned after nar_file deletion are cascade-deleted via FK constraints.
- OTel counters: `ncps_lru_narinfos_evicted_total`, `ncps_lru_nar_files_evicted_total`, `ncps_lru_chunks_evicted_total`, `ncps_lru_bytes_freed_total`.

---

## NarInfo Background Migration (Storage → Database)

Narinfos stored in the legacy storage backend (pre-0.8.0) are automatically migrated to the database:

1. `GetNarInfo` finds the narinfo in `narInfoStore` but not in the database.
2. It returns the narinfo to the client immediately.
3. A background goroutine acquires a distributed lock and writes the narinfo to the database.
4. If the lock is already held (another process migrating the same hash), the goroutine exits silently.

---

## Configuration Entry Points

| Method | Purpose |
|---|---|
| `Cache.SetCDCConfiguration(enabled, minSize, avgSize, maxSize)` | Enable CDC and configure chunker parameters |
| `Cache.SetChunkStore(cs chunk.Store)` | Inject chunk storage backend |
| `Cache.SetCDCLazyChunking(enabled, workers)` | Toggle lazy background chunking and worker count |
| `Cache.SetCDCDeleteDelay(duration)` | Delay before deleting whole-file NAR after CDC migration |
| `Cache.SetMaxSize(bytes)` | Maximum cache size (triggers LRU eviction) |
| `Cache.AddUpstreamCaches(...)` | Register upstream caches and their public keys |
| `Server.SetPutPermitted(bool)` | Allow/deny PUT requests |
| `Server.SetDeletePermitted(bool)` | Allow/deny DELETE requests |

---

## Observability

- **Tracing**: OpenTelemetry spans on every cache and server operation.
- **Metrics**: OTel meter with Prometheus export at `GET /metrics`.
  - `ncps_narinfo_served_total` (with `result`, `status`, `source` attributes)
  - `ncps_nar_served_total`
  - `ncps_lru_*_evicted_total` counters
  - `ncps_lru_bytes_freed_total`
  - `ncps_cache_utilization_ratio` gauge
  - `ncps_cache_max_size_bytes` gauge
  - `ncps_upstream_narinfo_fetch_duration`
  - `ncps_upstream_nar_fetch_duration`
  - `ncps_background_migration_objects_total`
- **Logging**: zerolog, context-propagated logger (`zerolog.Ctx(ctx)`).
- **Health**: `GET /healthz` via chi `middleware.Heartbeat`.
