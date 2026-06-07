# Architecture Specification

## Purpose

ncps (Nix Cache Proxy Server) is a Go HTTP proxy that sits between Nix clients and upstream binary caches (e.g., `cache.nixos.org`). It intercepts `.narinfo` and `.nar` requests, caches artifacts locally, and re-serves them—reducing external bandwidth and download latency. This specification describes the system's package structure, request lifecycle, concurrency model, database architecture, eviction strategy, background migration behavior, configuration entry points, and observability surface.

## Requirements

### Requirement: Package structure

The system SHALL be organized into the following packages, with each package owning a single, well-defined responsibility:

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

#### Scenario: Locating a responsibility by package

- **WHEN** a developer needs to find where a given responsibility lives (e.g., upstream HTTP fetching, S3 storage, or CDC chunking)
- **THEN** that responsibility resides in exactly one package as documented above (e.g., `pkg/cache/upstream`, `pkg/storage/s3`, `pkg/chunker` respectively)

### Requirement: Standard proxy request lifecycle (CDC disabled)

The system SHALL serve `.narinfo` and `.nar` requests through the chi router into the core cache, resolving each request via a cache-hit, legacy-hit, or upstream-miss path in that priority order.

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

#### Scenario: NarInfo request resolution

- **WHEN** a client issues `HEAD`/`GET /{hash}.narinfo`
- **THEN** `Cache.GetNarInfo` checks the `narinfos` table first (cache HIT returns immediately), then the storage backend via `NarInfoStore.HasNarInfo` (legacy HIT returns and triggers a background DB migration), and otherwise fetches from upstream, validates the signature, stores it in both the DB and storage, and returns it

#### Scenario: NAR request resolution

- **WHEN** a client issues `HEAD`/`GET /nar/{hash}.nar[.{compression}]`
- **THEN** `Cache.GetNar` checks the storage backend via `NarStore.HasNar` (HIT streams from storage), then the DB for CDC chunks via `HasNarInChunks` (HIT streams chunks progressively), and otherwise pulls from upstream into a temp file, moves it to storage, and stores a DB record

### Requirement: CDC enabled with lazy chunking disabled (synchronous chunking)

When CDC is enabled and `cdcLazyChunkingEnabled == false`, the system SHALL chunk a downloaded NAR synchronously before returning to the client, persist chunk records in progressive batches, and signal CDC storage by rewriting the narinfo URL.

The behavior is:

1. NAR is downloaded from upstream into a temp file (via `pullNarIntoStore`).
2. `storeNarWithCDC` is called **synchronously** before returning to the client.
3. The NAR is split into content-defined chunks by `chunker.CDCChunker`.
4. Each chunk is zstd-compressed and written to `chunk.Store`.
5. Chunk records and their `nar_file_chunks` links are written to the database in progressive batches (first batch after 100ms, subsequent batches every 500ms, max 100 chunks/batch).
6. Once all chunks are recorded, the whole-file NAR is deleted from `narStore` after `cdcDeleteDelay`.
7. `narinfo.URL` is rewritten to `{hash}.nar` (no compression extension) to signal CDC storage.

#### Scenario: Synchronous chunking on cache miss

- **WHEN** CDC is enabled, `cdcLazyChunkingEnabled == false`, and a NAR is fetched from upstream into a temp file
- **THEN** `storeNarWithCDC` runs synchronously before the client response: the NAR is split by `chunker.CDCChunker`, each chunk is zstd-compressed and written to `chunk.Store`, chunk and `nar_file_chunks` records are committed in progressive batches (first after 100ms, then every 500ms, max 100 chunks/batch), the whole-file NAR is deleted from `narStore` after `cdcDeleteDelay`, and `narinfo.URL` is rewritten to `{hash}.nar`

### Requirement: CDC enabled with lazy chunking enabled (background chunking)

When `cdcLazyChunkingEnabled == true`, the system SHALL store the NAR as a whole file, return to the client immediately, and chunk the NAR asynchronously in a bounded background worker pool while serving concurrent requests from the whole-file copy.

The behavior is:

1. NAR is downloaded and stored as a whole file in `narStore` first.
2. The handler returns to the client immediately.
3. A background goroutine (limited by `cdcBackgroundWorkers`) calls `storeNarWithCDC` asynchronously.
4. Concurrent requests for the same NAR during chunking are streamed from the whole-file storage until chunking completes.
5. A distributed lock (`TryLock`) prevents thundering-herd duplicate chunking of the same NAR across multiple processes.

#### Scenario: Background chunking with concurrent reads

- **WHEN** CDC is enabled, `cdcLazyChunkingEnabled == true`, and a NAR is fetched and stored as a whole file
- **THEN** the handler returns immediately, a background goroutine bounded by `cdcBackgroundWorkers` calls `storeNarWithCDC` asynchronously, concurrent requests for the same NAR are streamed from whole-file storage until chunking completes, and a distributed `TryLock` prevents duplicate chunking across processes

### Requirement: Concurrency model

The system SHALL coordinate concurrent work using the following mechanisms, each addressing a specific concern:

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

#### Scenario: Deduplicating concurrent downloads of the same narinfo

- **WHEN** multiple concurrent requests target the same narinfo hash that is being downloaded
- **THEN** a per-hash `downloadState` guarded by `sync.Mutex` + `sync.Cond` broadcast deduplicates the download so only one upstream fetch occurs and waiters are notified on completion

#### Scenario: Recovering a stale CDC lock

- **WHEN** a CDC chunking lock's `chunking_started_at` timestamp is older than 1 hour
- **THEN** the lock is considered stale and cleaned up so chunking can proceed

### Requirement: Pooled zstd readers/writers

To keep GC pauses predictable under concurrent load, the system SHALL reuse zstd encoders and decoders via `sync.Pool`-backed pooled types rather than allocating a fresh encoder/decoder per request.

NAR files are large binary streams (often hundreds of MB). When a client requests a NAR with `Accept-Encoding: zstd` but the cached copy is stored uncompressed, the server must re-compress on the fly. Naively allocating a new `zstd.Writer` per request would cause severe GC pressure at high throughput — each encoder allocates large internal dictionaries and buffers.

To address this, `pkg/zstd` provides pooled `PooledReader` and `PooledWriter` types backed by `sync.Pool`. Encoders and decoders are reset and returned to the pool after each use rather than being garbage-collected. This dramatically reduces allocations-per-request and keeps GC pauses predictable under concurrent load. The same pooling applies when decompressing chunks in CDC streaming (`GetChunk` via `chunk.Store`).

#### Scenario: Re-compressing an uncompressed NAR on the fly

- **WHEN** a client requests a NAR with `Accept-Encoding: zstd` but the cached copy is stored uncompressed
- **THEN** the server obtains a `PooledWriter` from `pkg/zstd`'s `sync.Pool`, re-compresses the stream, and resets and returns the encoder to the pool after use rather than garbage-collecting it

#### Scenario: Decompressing CDC chunks

- **WHEN** CDC chunks are streamed via `GetChunk` on `chunk.Store`
- **THEN** the same pooled `PooledReader` decoders are reused from `sync.Pool` to decompress each chunk

### Requirement: Database architecture

The system SHALL avoid ORMs and access the database exclusively through hand-written, engine-specific SQL compiled by sqlc into three independent querier implementations selected at runtime by URL scheme, with schema migrations managed by dbmate via a URL-aware wrapper.

ncps deliberately avoids ORMs. All database access goes through hand-written SQL queries stored in `db/query.{sqlite,postgres,mysql}.sql`. This keeps queries explicit, auditable, and engine-specific — we use the right SQL dialect per engine rather than a lowest-common-denominator abstraction.

**sqlc** reads those query files along with the migration-derived schema and generates three completely separate, engine-specific `Querier` interfaces and implementations:

- `pkg/database/sqlitedb` — SQLite-specific generated code; exploits SQLite's `RETURNING` clause and `ON CONFLICT` syntax.
- `pkg/database/postgresdb` — PostgreSQL-specific; uses `pgx/v5` driver, native `RETURNING`, and array types where applicable.
- `pkg/database/mysqldb` — MySQL/MariaDB-specific; handles `LAST_INSERT_ID()` and MySQL's lack of `RETURNING`.

The three packages are never mixed at runtime. `database.Open()` inspects the URL scheme and returns the matching engine wrapper as the shared `database.Querier` interface used by `pkg/cache`. This guarantees that engine-specific features (e.g., `RETURNING`, `ON CONFLICT`, transaction isolation) are used correctly per engine, not papered over.

**dbmate** manages schema migrations. The `dbmate` binary in dev and Docker is actually a thin Go wrapper (`nix/dbmate-wrapper/`) that reads the `--url` flag, auto-selects the migrations directory (`db/migrations/{sqlite,postgres,mysql}`) and schema output path (`db/schema/{engine}.sql`) from the URL scheme, and then delegates to the real `dbmate` binary. This means developers never need to specify `--migrations-dir` manually, and the same `dbmate` command works correctly across all three engines.

#### Scenario: Selecting the engine-specific querier at runtime

- **WHEN** `database.Open()` is called with a database URL
- **THEN** it inspects the URL scheme and returns the matching engine wrapper (`sqlitedb`, `postgresdb`, or `mysqldb`) as the shared `database.Querier` interface, never mixing the three engine packages at runtime

#### Scenario: Running dbmate against any engine

- **WHEN** the dbmate wrapper is invoked with a `--url` flag
- **THEN** it auto-selects the migrations directory (`db/migrations/{sqlite,postgres,mysql}`) and schema output path (`db/schema/{engine}.sql`) from the URL scheme and delegates to the real `dbmate` binary, without requiring a manual `--migrations-dir`

### Requirement: LRU eviction

The system SHALL bound on-disk cache size via a scheduled least-recently-used eviction job that deletes the least-used NARs (chunks or whole files) and their orphaned narinfos, and SHALL emit eviction counters.

- Controlled by `Cache.SetMaxSize(bytes)`.
- Scheduled via `Cache.AddLRUCronJob(ctx, schedule)` using an internal cron runner.
- `runLRU` queries `GetLeastUsedNarInfos` from the database (ordered by `last_accessed_at`).
- For each evicted `nar_file`: deletes chunks (CDC) or whole file (non-CDC) from storage, removes DB records.
- `narinfos` orphaned after nar_file deletion are cascade-deleted via FK constraints.
- OTel counters: `ncps_lru_narinfos_evicted_total`, `ncps_lru_nar_files_evicted_total`, `ncps_lru_chunks_evicted_total`, `ncps_lru_bytes_freed_total`.

#### Scenario: Evicting least-recently-used entries

- **WHEN** the LRU cron job scheduled by `Cache.AddLRUCronJob` runs and the cache exceeds the `Cache.SetMaxSize` limit
- **THEN** `runLRU` queries `GetLeastUsedNarInfos` ordered by `last_accessed_at`, deletes each evicted `nar_file`'s chunks (CDC) or whole file (non-CDC) and its DB records, cascade-deletes orphaned `narinfos` via FK constraints, and increments the `ncps_lru_*_evicted_total` and `ncps_lru_bytes_freed_total` counters

### Requirement: NarInfo background migration (storage to database)

Narinfos stored in the legacy storage backend (pre-0.8.0) SHALL be automatically migrated to the database without blocking the client response, using a distributed lock to avoid duplicate migration across processes.

1. `GetNarInfo` finds the narinfo in `narInfoStore` but not in the database.
2. It returns the narinfo to the client immediately.
3. A background goroutine acquires a distributed lock and writes the narinfo to the database.
4. If the lock is already held (another process migrating the same hash), the goroutine exits silently.

#### Scenario: Migrating a legacy narinfo on read

- **WHEN** `GetNarInfo` finds a narinfo in `narInfoStore` but not in the database
- **THEN** it returns the narinfo to the client immediately and a background goroutine acquires a distributed lock to write it to the database, exiting silently if the lock is already held by another process migrating the same hash

### Requirement: Configuration entry points

The system SHALL expose the following methods as the supported entry points for configuring cache and server behavior:

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

#### Scenario: Enabling CDC with lazy chunking

- **WHEN** an operator calls `Cache.SetCDCConfiguration(...)`, `Cache.SetChunkStore(...)`, and `Cache.SetCDCLazyChunking(enabled, workers)`
- **THEN** CDC is enabled with the configured chunker parameters, the chunk storage backend is injected, and lazy background chunking is toggled with the specified worker count

#### Scenario: Controlling write verbs on the server

- **WHEN** an operator calls `Server.SetPutPermitted(bool)` and `Server.SetDeletePermitted(bool)`
- **THEN** the server allows or denies `PUT` and `DELETE` requests accordingly

### Requirement: Observability

The system SHALL emit OpenTelemetry traces and metrics, expose a Prometheus endpoint, use context-propagated structured logging, and expose a health endpoint.

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

#### Scenario: Scraping metrics

- **WHEN** a Prometheus scraper issues `GET /metrics`
- **THEN** the OTel meter exports the documented counters, histograms, and gauges (e.g., `ncps_narinfo_served_total`, `ncps_nar_served_total`, `ncps_lru_*_evicted_total`, `ncps_cache_utilization_ratio`, `ncps_upstream_nar_fetch_duration`)

#### Scenario: Health check

- **WHEN** a client issues `GET /healthz`
- **THEN** the chi `middleware.Heartbeat` responds indicating the server is healthy
