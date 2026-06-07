# API Surface Specification

## Purpose

This specification documents the public API of ncps. The API consists of three layers: (1) the Nix-protocol-compatible HTTP endpoints served by `pkg/server`, (2) the `pkg/cache.Cache` domain methods called by the server handlers, and (3) the pluggable storage and chunk backend interfaces in `pkg/storage` and `pkg/storage/chunk`. It also documents the central artifact type `narinfo.NarInfo`, the `pkg/nar` URL type, and the known edge cases and failure modes that the system must handle.

## Requirements

### Requirement: NarInfo Data Structure

The system SHALL represent every narinfo as the `narinfo.NarInfo` struct from `github.com/nix-community/go-nix/pkg/narinfo`, and all narinfo operations SHALL revolve around this struct.

The central artifact type is `narinfo.NarInfo`:

```go
// NarInfo represents a Nix narinfo file — the metadata record for a single
// store path in the Nix binary cache protocol.
type NarInfo struct {
    StorePath   string      // Absolute /nix/store path this narinfo describes
    URL         string      // Relative NAR URL (e.g., "nar/abc123.nar.xz")
    Compression string      // Compression type: "none", "xz", "zstd", "brotli"
    FileHash    string      // Hash of the compressed NAR file (e.g., "sha256:abc...")
    FileSize    uint64      // Size of the compressed NAR file in bytes
    NarHash     string      // Hash of the uncompressed NAR (e.g., "sha256:def...")
    NarSize     uint64      // Size of the uncompressed NAR in bytes
    References  []string    // Other store paths this path depends on (base names only)
    Deriver     string      // Optional: store path of the derivation that built this
    System      string      // Optional: target system (e.g., "x86_64-linux")
    CA          string      // Optional: content-addressed field
    Signatures  []signature.Signature // Ed25519 signatures (keyname:base64)
}
```

Key invariants:
- `URL` is always normalized before serving (narinfo-hash prefix stripped via `nar.URL.Normalize()`).
- `References` elements are bare base names (no `/nix/store/` prefix), stored one-per-row in `narinfo_references`.
- `Signatures` are stored one-per-row in `narinfo_signatures` and re-attached on read.

#### Scenario: URL normalization on serve
- **WHEN** a narinfo is read for serving
- **THEN** the system SHALL normalize the `URL` field via `nar.URL.Normalize()`, stripping any narinfo-hash prefix

#### Scenario: References and signatures persisted per-row
- **WHEN** a narinfo is stored
- **THEN** the system SHALL store `References` as bare base names one-per-row in `narinfo_references` and `Signatures` one-per-row in `narinfo_signatures`, re-attaching the signatures on read

### Requirement: Infrastructure Routes

The system SHALL register infrastructure routes on a `chi.Mux` router in `pkg/server.Server.createRouter()`.

| Method | Path | Handler | Notes |
|---|---|---|---|
| `GET` | `/healthz` | chi `middleware.Heartbeat` | Returns `200 OK` with body `"."`. Never traced. |
| `GET` | `/metrics` | `promhttp.HandlerFor` | Only registered when a Prometheus gatherer is configured. Never traced. |

#### Scenario: Health check
- **WHEN** a client sends `GET /healthz`
- **THEN** the system SHALL return `200 OK` with body `"."` and SHALL NOT emit a trace

#### Scenario: Metrics endpoint
- **WHEN** a Prometheus gatherer is configured and a client sends `GET /metrics`
- **THEN** the system SHALL serve the Prometheus metrics via `promhttp.HandlerFor` and SHALL NOT emit a trace
- **WHEN** no Prometheus gatherer is configured
- **THEN** the system SHALL NOT register the `/metrics` route

### Requirement: GET /nix-cache-info

The system SHALL return the Nix cache configuration text at `GET /nix-cache-info`.

**Response:** `200 OK`, `Content-Type: text/plain`

```
StoreDir: /nix/store
WantMassQuery: 1
Priority: 10
```

#### Scenario: Cache info served
- **WHEN** a client sends `GET /nix-cache-info`
- **THEN** the system SHALL return `200 OK` with `Content-Type: text/plain` and a body declaring `StoreDir: /nix/store`, `WantMassQuery: 1`, and `Priority: 10`

### Requirement: GET /pubkey

The system SHALL return the cache's Ed25519 public key at `GET /pubkey`, used for narinfo signature verification by Nix clients.

**Response:** `200 OK`, `Content-Type: text/plain`, body is the base64-encoded public key.

#### Scenario: Public key served
- **WHEN** a client sends `GET /pubkey`
- **THEN** the system SHALL return `200 OK` with `Content-Type: text/plain` and a body containing the base64-encoded Ed25519 public key

### Requirement: HEAD and GET /{hash}.narinfo

The system SHALL serve narinfo metadata for a store path hash at `HEAD /{hash}.narinfo` and `GET /{hash}.narinfo`.

- `hash` must match `narinfo.HashPattern` (Nix base32).
- `HEAD` returns headers only (no body).
- `GET` returns full narinfo text.

**Response:** `200 OK`, `Content-Type: text/x-nix-narinfo`

The NAR URL in the returned narinfo is always normalized (any narinfo-hash prefix stripped).

**Failure modes:**
- `404 Not Found` — hash not in database, storage, or any upstream cache.
- `500 Internal Server Error` — database error or signature validation failure.

#### Scenario: Successful narinfo fetch
- **WHEN** a client sends `GET /{hash}.narinfo` for a hash matching `narinfo.HashPattern` that is available in database, storage, or an upstream cache
- **THEN** the system SHALL return `200 OK` with `Content-Type: text/x-nix-narinfo` and the full narinfo text, with the NAR URL normalized

#### Scenario: HEAD returns headers only
- **WHEN** a client sends `HEAD /{hash}.narinfo` for an available narinfo
- **THEN** the system SHALL return `200 OK` with the narinfo headers and no body

#### Scenario: Narinfo not found
- **WHEN** the hash is not present in the database, storage, or any upstream cache
- **THEN** the system SHALL return `404 Not Found`

#### Scenario: Narinfo server error
- **WHEN** a database error or signature validation failure occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: PUT /upload/{hash}.narinfo

The system SHALL store a narinfo pushed by the client at `PUT /upload/{hash}.narinfo`, available only under the `/upload` prefix.

**Request:** `Content-Type: text/x-nix-narinfo`, body is narinfo text.

**Response:** `204 No Content` on success.

**Authorization:** `405 Method Not Allowed` is evaluated by a strict configuration boolean — there is no auth middleware or token validation. `Server.SetPutPermitted(false)` (the default) causes the handler to immediately return `405` before any body is read. There is no per-request authentication.

**Failure modes:**
- `405 Method Not Allowed` — PUT not permitted (`putPermitted == false`).
- `500 Internal Server Error` — parse or storage error.

#### Scenario: Successful narinfo upload
- **WHEN** `putPermitted == true` and the client sends a valid narinfo body to `PUT /upload/{hash}.narinfo`
- **THEN** the system SHALL store the narinfo and return `204 No Content`

#### Scenario: Narinfo upload not permitted
- **WHEN** `putPermitted == false` (the default)
- **THEN** the system SHALL return `405 Method Not Allowed` before any body is read

#### Scenario: Narinfo upload error
- **WHEN** a parse or storage error occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: DELETE /{hash}.narinfo

The system SHALL delete a cached narinfo and its associated nar_file records at `DELETE /{hash}.narinfo`.

**Response:** `204 No Content` on success.

**Authorization:** Same as PUT — `deletePermitted` is a strict boolean set at startup via `Server.SetDeletePermitted`. No middleware or token evaluation occurs; a `false` value causes an immediate `405` response.

**Failure modes:**
- `405 Method Not Allowed` — DELETE not permitted (`deletePermitted == false`).
- `404 Not Found` — hash not found.
- `500 Internal Server Error` — database or storage error.

#### Scenario: Successful narinfo deletion
- **WHEN** `deletePermitted == true` and the narinfo exists
- **THEN** the system SHALL delete the narinfo and its associated nar_file records and return `204 No Content`

#### Scenario: Narinfo deletion not permitted
- **WHEN** `deletePermitted == false`
- **THEN** the system SHALL return `405 Method Not Allowed` immediately

#### Scenario: Narinfo deletion not found
- **WHEN** the hash is not found
- **THEN** the system SHALL return `404 Not Found`

#### Scenario: Narinfo deletion error
- **WHEN** a database or storage error occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: HEAD and GET /nar/{hash}.nar[.{compression}]

The system SHALL serve a NAR archive at `HEAD`/`GET /nar/{hash}.nar` and `HEAD`/`GET /nar/{hash}.nar.{compression}`, supporting uncompressed (`.nar`) and compressed (`.nar.xz`, `.nar.zst`, etc.) variants.

- `hash` must match `nar.NormalizedHashPattern`.
- `compression` is optional; omitting it serves the raw NAR stream.

**Response:** `200 OK`, `Content-Type: application/x-nix-nar`

- If CDC is enabled and the NAR is stored as chunks: streams chunks progressively as they become available.
- If stored as a whole file: pipes directly from storage.
- `Content-Length` is set from the `nar_files.file_size` database record.

**Transparent zstd re-compression:** If the client sends `Accept-Encoding: zstd` and the stored NAR is uncompressed, the server transparently zstd-compresses the response on the fly using a pooled `zstd.Writer` (see `pkg/zstd`).

**Client disconnects:** If the Nix client drops the connection mid-download, `ctx.Done()` is signalled. The behaviour depends on where the download is at:
- **Serving from storage or chunks:** The pipe/stream is aborted immediately; no background work continues.
- **Upstream fetch in progress:** The upstream HTTP response body read is context-aware. When `ctx` is cancelled the download loop exits, the partial temp file is discarded, and **no** DB record or storage file is written. The NAR is not cached.
- **Exception — lazy CDC background goroutine:** If CDC lazy chunking was already triggered (the whole-file NAR was written to `narStore` and the background goroutine dispatched) *before* the client disconnected, the background goroutine runs to completion using a detached context (`context.Background()`), so the NAR is still fully chunked and cached even though the triggering request was cancelled.

**Failure modes:**
- `404 Not Found` — NAR not in storage, chunks, or any upstream.
- `500 Internal Server Error` — I/O error, upstream failure, or hash mismatch.
- Connection reset / partial response — client disconnected; upstream fetch aborted (see above).

#### Scenario: Serve NAR from chunks
- **WHEN** CDC is enabled and the NAR is stored as chunks
- **THEN** the system SHALL return `200 OK` with `Content-Type: application/x-nix-nar`, streaming chunks progressively as they become available, with `Content-Length` from the `nar_files.file_size` record

#### Scenario: Serve NAR from whole file
- **WHEN** the NAR is stored as a whole file
- **THEN** the system SHALL return `200 OK` with `Content-Type: application/x-nix-nar`, piping directly from storage, with `Content-Length` from the `nar_files.file_size` record

#### Scenario: Transparent zstd re-compression
- **WHEN** the client sends `Accept-Encoding: zstd` and the stored NAR is uncompressed
- **THEN** the system SHALL transparently zstd-compress the response on the fly using a pooled `zstd.Writer`

#### Scenario: Client disconnects while serving from storage or chunks
- **WHEN** the client drops the connection while the system is serving from storage or chunks
- **THEN** the system SHALL abort the pipe/stream immediately and SHALL NOT continue any background work

#### Scenario: Client disconnects during upstream fetch
- **WHEN** the client drops the connection while an upstream fetch is in progress
- **THEN** the system SHALL exit the download loop on `ctx` cancellation, discard the partial temp file, write no DB record or storage file, and not cache the NAR

#### Scenario: Client disconnects after lazy CDC dispatch
- **WHEN** the client disconnects after the whole-file NAR was written to `narStore` and the lazy CDC background goroutine was already dispatched
- **THEN** the background goroutine SHALL run to completion using a detached `context.Background()`, fully chunking and caching the NAR despite the cancelled triggering request

#### Scenario: NAR not found
- **WHEN** the NAR is not in storage, chunks, or any upstream
- **THEN** the system SHALL return `404 Not Found`

#### Scenario: NAR serving error
- **WHEN** an I/O error, upstream failure, or hash mismatch occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: PUT /upload/nar/{hash}.nar[.{compression}]

The system SHALL store a NAR pushed by the client at `PUT /upload/nar/{hash}.nar[.{compression}]`.

**Request:** `Content-Type: application/x-nix-nar`, body is raw NAR bytes.

**Response:** `204 No Content` on success.

**Failure modes:**
- `405 Method Not Allowed` — PUT not permitted.
- `500 Internal Server Error` — storage error.

#### Scenario: Successful NAR upload
- **WHEN** PUT is permitted and the client sends raw NAR bytes
- **THEN** the system SHALL store the NAR and return `204 No Content`

#### Scenario: NAR upload not permitted
- **WHEN** PUT is not permitted
- **THEN** the system SHALL return `405 Method Not Allowed`

#### Scenario: NAR upload error
- **WHEN** a storage error occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: DELETE /nar/{hash}.nar[.{compression}]

The system SHALL delete a cached NAR from storage and the database at `DELETE /nar/{hash}.nar[.{compression}]`.

**Response:** `204 No Content` on success.

**Failure modes:**
- `405 Method Not Allowed` — DELETE not permitted.
- `404 Not Found`.
- `500 Internal Server Error`.

#### Scenario: Successful NAR deletion
- **WHEN** DELETE is permitted and the NAR exists
- **THEN** the system SHALL delete the NAR from storage and the database and return `204 No Content`

#### Scenario: NAR deletion not permitted
- **WHEN** DELETE is not permitted
- **THEN** the system SHALL return `405 Method Not Allowed`

#### Scenario: NAR deletion not found
- **WHEN** the NAR does not exist
- **THEN** the system SHALL return `404 Not Found`

#### Scenario: NAR deletion error
- **WHEN** a storage or database error occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: HEAD /build-trace-v2/{drvName}/{outputName}.doi

The system SHALL return `200 OK` if the build trace entry exists or `404 Not Found` if it does not at `HEAD /build-trace-v2/{drvName}/{outputName}.doi`. No body is returned.

#### Scenario: Entry exists
- **WHEN** a client sends `HEAD /build-trace-v2/{drvName}/{outputName}.doi` for a stored entry
- **THEN** the system SHALL return `200 OK` with no body

#### Scenario: Entry does not exist
- **WHEN** a client sends `HEAD /build-trace-v2/{drvName}/{outputName}.doi` for an unknown entry
- **THEN** the system SHALL return `404 Not Found`

### Requirement: GET /build-trace-v2/{drvName}/{outputName}.doi

The system SHALL return the stored build trace entry as JSON at `GET /build-trace-v2/{drvName}/{outputName}.doi`.

**Response:** `Content-Type: application/json`, body is the build trace v3 JSON object with `key` and `value` fields, `value.signatures` containing all stored signatures including ncps's own.

**Failure modes:**
- `404 Not Found` — entry not found.
- `500 Internal Server Error` — database error.

#### Scenario: Successful GET
- **WHEN** a client sends `GET /build-trace-v2/{drvName}/{outputName}.doi` and the entry exists
- **THEN** the system SHALL return `200 OK` with a JSON body containing `key` and `value` fields

#### Scenario: Not found
- **WHEN** a client sends `GET /build-trace-v2/{drvName}/{outputName}.doi` for an unknown entry
- **THEN** the system SHALL return `404 Not Found`

#### Scenario: Build trace GET error
- **WHEN** a database error occurs
- **THEN** the system SHALL return `500 Internal Server Error`

### Requirement: PUT /upload/build-trace-v2/{drvName}/{outputName}.doi

The system SHALL store a build trace entry at `PUT /upload/build-trace-v2/{drvName}/{outputName}.doi`, available only under the `/upload` prefix. Authorization follows the same `putPermitted` boolean gate used by narinfo and NAR uploads.

**Request:** `Content-Type: application/json`, body is a build trace v3 JSON object.

**Response:** `204 No Content` on success. (Consistent with `putNarInfo` and `putNar`.)

**Authorization:** `putPermitted == false` (the default) causes an immediate `405 Method Not Allowed` response before any body is read. No per-request authentication. (Consistent with existing narinfo/NAR upload behavior.)

**Failure modes:**
- `400 Bad Request` — malformed JSON or URL/body mismatch.
- `405 Method Not Allowed` — PUT not permitted.
- `500 Internal Server Error` — database error.

#### Scenario: Successful PUT
- **WHEN** `putPermitted == true` and the client sends a valid build trace JSON body
- **THEN** the system SHALL return `204 No Content`

#### Scenario: PUT not permitted
- **WHEN** `putPermitted == false`
- **THEN** the system SHALL return `405 Method Not Allowed`

#### Scenario: Invalid body
- **WHEN** the body is not valid JSON or missing required fields
- **THEN** the system SHALL return `400 Bad Request`

### Requirement: Cache NarInfo Operations

The `pkg/cache.Cache` type SHALL expose the core narinfo domain methods called by the server handlers.

```go
// GetNarInfo returns narinfo for the given hash.
// Lookup order: database → storage (legacy) → upstream.
// Side effects:
//   - Database miss + storage hit: spawns background DB migration goroutine.
//   - Database hit + CDC eligible: spawns maybeBackgroundMigrateNarToChunks.
func (c *Cache) GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error)

// PutNarInfo stores a narinfo from an io.ReadCloser.
// Parses, validates, stores in DB (narinfos + references + signatures + narinfo_nar_files).
func (c *Cache) PutNarInfo(ctx context.Context, hash string, r io.ReadCloser) error

// DeleteNarInfo removes a narinfo from the database and storage.
func (c *Cache) DeleteNarInfo(ctx context.Context, hash string) error
```

#### Scenario: GetNarInfo lookup order
- **WHEN** `GetNarInfo` is called for a hash
- **THEN** the system SHALL look up the narinfo in order database → storage (legacy) → upstream
- **WHEN** the database misses but storage hits
- **THEN** the system SHALL spawn a background DB migration goroutine
- **WHEN** the database hits and the NAR is CDC eligible
- **THEN** the system SHALL spawn `maybeBackgroundMigrateNarToChunks`

#### Scenario: PutNarInfo persistence
- **WHEN** `PutNarInfo` is called with a narinfo reader
- **THEN** the system SHALL parse and validate the narinfo and store it in the database (narinfos + references + signatures + narinfo_nar_files)

#### Scenario: DeleteNarInfo removal
- **WHEN** `DeleteNarInfo` is called for a hash
- **THEN** the system SHALL remove the narinfo from the database and storage

### Requirement: Cache NAR Operations

The `pkg/cache.Cache` type SHALL expose the core NAR domain methods called by the server handlers.

```go
// GetNar streams a NAR to the provided writer.
// Lookup order (CDC disabled): storage → upstream.
// Lookup order (CDC enabled): chunks (DB) → storage → upstream.
// An upstream fetch: downloads to temp file, atomically moves to storage,
// writes DB record, optionally triggers CDC chunking.
func (c *Cache) GetNar(ctx context.Context, narURL nar.URL, w io.Writer) error

// PutNar stores a NAR from an io.ReadCloser.
func (c *Cache) PutNar(ctx context.Context, narURL nar.URL, r io.ReadCloser) error

// DeleteNar removes a NAR from storage and the database.
func (c *Cache) DeleteNar(ctx context.Context, narURL nar.URL) error

// HasNarInChunks checks the database for CDC chunk records.
func (c *Cache) HasNarInChunks(ctx context.Context, narURL nar.URL) (bool, error)
```

#### Scenario: GetNar lookup order
- **WHEN** `GetNar` is called with CDC disabled
- **THEN** the system SHALL look up the NAR in order storage → upstream
- **WHEN** `GetNar` is called with CDC enabled
- **THEN** the system SHALL look up the NAR in order chunks (DB) → storage → upstream

#### Scenario: GetNar upstream fetch
- **WHEN** `GetNar` must fetch from upstream
- **THEN** the system SHALL download to a temp file, atomically move it to storage, write the DB record, and optionally trigger CDC chunking

#### Scenario: PutNar and DeleteNar
- **WHEN** `PutNar` is called
- **THEN** the system SHALL store the NAR from the reader
- **WHEN** `DeleteNar` is called
- **THEN** the system SHALL remove the NAR from storage and the database

#### Scenario: HasNarInChunks check
- **WHEN** `HasNarInChunks` is called
- **THEN** the system SHALL check the database for CDC chunk records and return whether they exist

### Requirement: Cache CDC Configuration

The `pkg/cache.Cache` type SHALL expose methods to configure Content-Defined Chunking (CDC).

```go
// SetCDCConfiguration enables CDC and sets chunker parameters.
// minSize, avgSize, maxSize are in bytes.
func (c *Cache) SetCDCConfiguration(enabled bool, minSize, avgSize, maxSize uint32) error

// SetChunkStore injects the chunk storage backend.
func (c *Cache) SetChunkStore(cs chunk.Store)

// SetCDCLazyChunking toggles lazy background chunking.
// workers controls the goroutine pool size.
func (c *Cache) SetCDCLazyChunking(enabled bool, workers int)

// SetCDCDeleteDelay sets the delay before deleting whole-file NARs after chunking.
func (c *Cache) SetCDCDeleteDelay(delay time.Duration)
```

#### Scenario: Configure CDC parameters
- **WHEN** `SetCDCConfiguration` is called
- **THEN** the system SHALL enable CDC and set the chunker `minSize`, `avgSize`, and `maxSize` parameters in bytes

#### Scenario: Inject chunk store and lazy chunking
- **WHEN** `SetChunkStore` is called
- **THEN** the system SHALL inject the chunk storage backend
- **WHEN** `SetCDCLazyChunking` is called
- **THEN** the system SHALL toggle lazy background chunking with the given worker pool size
- **WHEN** `SetCDCDeleteDelay` is called
- **THEN** the system SHALL set the delay before deleting whole-file NARs after chunking

### Requirement: Cache Lifecycle Operations

The `pkg/cache.Cache` type SHALL expose lifecycle methods for cron scheduling, sizing, and upstream registration.

```go
// SetupCron initializes the cron runner with the given timezone.
func (c *Cache) SetupCron(ctx context.Context, timezone *time.Location)

// AddLRUCronJob registers an LRU eviction job on the given schedule.
func (c *Cache) AddLRUCronJob(ctx context.Context, schedule cron.Schedule)

// AddCDCDeletedCleanupCronJob registers a CDC orphan-cleanup job.
func (c *Cache) AddCDCDeletedCleanupCronJob(ctx context.Context, schedule cron.Schedule)

// SetMaxSize sets the maximum cache size in bytes for LRU eviction.
func (c *Cache) SetMaxSize(maxSize uint64)

// AddUpstreamCaches registers upstream caches and their trusted public keys.
func (c *Cache) AddUpstreamCaches(...)
```

#### Scenario: Cron setup and jobs
- **WHEN** `SetupCron` is called with a timezone
- **THEN** the system SHALL initialize the cron runner with that timezone
- **WHEN** `AddLRUCronJob` is called
- **THEN** the system SHALL register an LRU eviction job on the given schedule
- **WHEN** `AddCDCDeletedCleanupCronJob` is called
- **THEN** the system SHALL register a CDC orphan-cleanup job on the given schedule

#### Scenario: Sizing and upstream registration
- **WHEN** `SetMaxSize` is called
- **THEN** the system SHALL set the maximum cache size in bytes for LRU eviction
- **WHEN** `AddUpstreamCaches` is called
- **THEN** the system SHALL register the given upstream caches and their trusted public keys

### Requirement: Storage Interfaces

The `pkg/storage` package SHALL define the pluggable backend contracts implemented by the filesystem and S3 backends.

```go
// NarInfoStore — narinfo metadata storage (filesystem or S3).
type NarInfoStore interface {
    HasNarInfo(ctx context.Context, hash string) bool
    GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error)
    PutNarInfo(ctx context.Context, hash string, ni *narinfo.NarInfo) error
    DeleteNarInfo(ctx context.Context, hash string) error
}

// NarStore — NAR archive storage (filesystem or S3).
type NarStore interface {
    HasNar(ctx context.Context, narURL nar.URL) bool
    GetNar(ctx context.Context, narURL nar.URL) (io.ReadCloser, error)
    PutNar(ctx context.Context, narURL nar.URL) (io.WriteCloser, error)
    DeleteNar(ctx context.Context, narURL nar.URL) error
    GetNarSize(ctx context.Context, narURL nar.URL) (int64, error)
}

// ConfigStore — deprecated; config is now stored in the database.
// Deprecated: Use database config table via Querier.
type ConfigStore interface {
    GetSecretKey(ctx context.Context) (signature.SecretKey, error)
    PutSecretKey(ctx context.Context, sk signature.SecretKey) error
    DeleteSecretKey(ctx context.Context) error
}
```

Sentinel errors:
- `storage.ErrNotFound` — returned when a narinfo or NAR does not exist.
- `storage.ErrAlreadyExists` — returned when attempting to overwrite an existing file.

#### Scenario: Backends implement storage interfaces
- **WHEN** a storage backend is selected (filesystem or S3)
- **THEN** the backend SHALL implement `NarInfoStore` and `NarStore` (and the deprecated `ConfigStore`)

#### Scenario: Storage sentinel errors
- **WHEN** a narinfo or NAR does not exist
- **THEN** the store SHALL return `storage.ErrNotFound`
- **WHEN** an attempt is made to overwrite an existing file
- **THEN** the store SHALL return `storage.ErrAlreadyExists`

### Requirement: Chunk Store Interface

The `pkg/storage/chunk` package SHALL define the `Store` interface for CDC chunk storage, implemented by `chunk.NewLocalStore(baseDir)` and `chunk.NewS3Store(ctx, cfg, locker)`.

```go
type Store interface {
    HasChunk(ctx context.Context, hash string) (bool, error)
    // GetChunk decompresses before returning.
    GetChunk(ctx context.Context, hash string) (io.ReadCloser, error)
    // GetRawChunk returns compressed bytes without decompression.
    GetRawChunk(ctx context.Context, hash string) (io.ReadCloser, error)
    // PutChunk stores a chunk. Returns (isNew, compressedSize, error).
    PutChunk(ctx context.Context, hash string, data []byte) (bool, int64, error)
    DeleteChunk(ctx context.Context, hash string) error
    WalkChunks(ctx context.Context, fn func(hash string) error) error
}
```

#### Scenario: Chunk retrieval variants
- **WHEN** `GetChunk` is called
- **THEN** the store SHALL decompress the chunk before returning it
- **WHEN** `GetRawChunk` is called
- **THEN** the store SHALL return the compressed bytes without decompression

#### Scenario: Chunk storage result
- **WHEN** `PutChunk` is called
- **THEN** the store SHALL store the chunk and return `(isNew, compressedSize, error)`

#### Scenario: Chunk store implementations
- **WHEN** a chunk store is constructed via `chunk.NewLocalStore(baseDir)` or `chunk.NewS3Store(ctx, cfg, locker)`
- **THEN** the resulting value SHALL implement the `Store` interface

### Requirement: NAR URL Type

The `pkg/nar` package SHALL define the `URL` type and parsing functions that represent and normalize narinfo URL fields.

```go
type URL struct {
    Hash            string          // Nix base32 hash (normalized, 52 chars)
    Compression     CompressionType // none, xz, zstd, brotli, ...
    Query           url.Values      // passthrough query params
    TransparentZstd bool            // client requested transparent zstd encoding
}

// ParseURL parses a narinfo URL field into a URL struct.
// Validates hash against nar.NormalizedHashPattern.
// Strips narinfo-hash prefix if present (e.g., "prefix-actualhash" → "actualhash").
func ParseURL(u string) (URL, error)

// Normalize returns a URL with any embedded narinfo-hash prefix stripped.
func (u URL) Normalize() URL
```

#### Scenario: ParseURL validation and prefix stripping
- **WHEN** `ParseURL` is called with a narinfo URL field
- **THEN** the system SHALL validate the hash against `nar.NormalizedHashPattern` and strip any narinfo-hash prefix (e.g., `"prefix-actualhash"` → `"actualhash"`)

#### Scenario: Normalize strips embedded prefix
- **WHEN** `Normalize` is called on a `URL`
- **THEN** the system SHALL return a `URL` with any embedded narinfo-hash prefix stripped

### Requirement: Known Edge Cases and Failure Modes

The system SHALL handle the following edge cases and failure modes with the documented behavior.

| Scenario | Behavior |
|---|---|
| Upstream cache miss (all upstreams return 404) | `GetNarInfo` / `GetNar` return `storage.ErrNotFound`; server responds `404`. |
| Client disconnects mid-upstream-fetch | `ctx` cancellation exits the download loop; partial temp file discarded; NAR not cached. Exception: if lazy CDC background goroutine already dispatched, it runs to completion on a detached context. |
| Upstream fetch timeout / network error | Error propagated; server responds `500`. |
| NAR hash mismatch after download | Download is discarded; error returned; server responds `500`. |
| Concurrent `PutNarInfo` + background migration (same hash) | Duplicate key error handled gracefully; first commit wins; second gets `storage.ErrAlreadyExists`. |
| Concurrent `GetNarInfo` thundering herd (same hash) | `downloadState` serializes; only first goroutine fetches from upstream; others wait and receive the result via broadcast. |
| CDC chunking in progress + client request for same NAR | Client is served from whole-file storage during chunking. Background goroutine is unaffected by the client's context. |
| Stale CDC lock (process crash mid-chunk) | `chunking_started_at` age ≥ 1h triggers stale-lock recovery: `DeleteNarFileChunksByNarFileID` removes junction records; `cleanupStaleLockChunks` removes orphaned chunk files+records; chunking restarts. |
| NarInfo in database but NAR not in storage or chunks | `GetNar` falls through to upstream fetch; re-downloads and re-stores the NAR. |
| NarInfo `url` field is `NULL` in database | `getNarInfoFromDatabase` treats the record as incomplete and falls through to storage lookup. |
| SQLite write contention | WAL mode + `busy_timeout=10s` retries; `MaxOpenConns=1` serializes writes. |
| Storage `ErrAlreadyExists` on `PutNarInfo` | Treated as a no-op success (idempotent). |
| Upload-only context (`/upload` prefix) | `cache.WithUploadOnly(ctx)` is set; cache layer skips upstream fetch for PUTs. |

#### Scenario: Upstream cache miss
- **WHEN** all upstream caches return 404
- **THEN** `GetNarInfo` / `GetNar` SHALL return `storage.ErrNotFound` and the server SHALL respond `404`

#### Scenario: Upstream fetch failure
- **WHEN** an upstream fetch times out, errors on the network, or the NAR hash mismatches after download
- **THEN** the system SHALL propagate the error (discarding any partial download) and the server SHALL respond `500`

#### Scenario: Concurrent narinfo writes
- **WHEN** a `PutNarInfo` races with a background migration for the same hash
- **THEN** the system SHALL handle the duplicate key error gracefully, the first commit SHALL win, and the second SHALL receive `storage.ErrAlreadyExists`

#### Scenario: Thundering herd on GetNarInfo
- **WHEN** multiple concurrent `GetNarInfo` calls target the same hash
- **THEN** `downloadState` SHALL serialize them so only the first goroutine fetches from upstream and the others wait and receive the result via broadcast

#### Scenario: CDC chunking in progress
- **WHEN** a client requests a NAR while CDC chunking is in progress for it
- **THEN** the system SHALL serve the client from whole-file storage during chunking, and the background goroutine SHALL be unaffected by the client's context

#### Scenario: Stale CDC lock recovery
- **WHEN** `chunking_started_at` age is ≥ 1h after a process crash mid-chunk
- **THEN** the system SHALL trigger stale-lock recovery: `DeleteNarFileChunksByNarFileID` removes junction records, `cleanupStaleLockChunks` removes orphaned chunk files and records, and chunking restarts

#### Scenario: NarInfo present but NAR missing
- **WHEN** a narinfo exists in the database but the NAR is not in storage or chunks
- **THEN** `GetNar` SHALL fall through to an upstream fetch, re-downloading and re-storing the NAR

#### Scenario: NarInfo url field is NULL
- **WHEN** the narinfo `url` field is `NULL` in the database
- **THEN** `getNarInfoFromDatabase` SHALL treat the record as incomplete and fall through to storage lookup

#### Scenario: SQLite write contention
- **WHEN** concurrent writes contend on SQLite
- **THEN** the system SHALL use WAL mode with `busy_timeout=10s` retries and `MaxOpenConns=1` to serialize writes

#### Scenario: Idempotent PutNarInfo
- **WHEN** `PutNarInfo` receives `storage.ErrAlreadyExists`
- **THEN** the system SHALL treat it as a no-op success (idempotent)

#### Scenario: Upload-only context skips upstream
- **WHEN** a request is served under the `/upload` prefix with `cache.WithUploadOnly(ctx)` set
- **THEN** the cache layer SHALL skip the upstream fetch for PUTs
