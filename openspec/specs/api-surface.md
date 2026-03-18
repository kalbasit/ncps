# API Surface Specification

## Overview

The public API of ncps consists of three layers:

1. **HTTP endpoints** — the Nix-protocol-compatible REST API served by `pkg/server`.
2. **`pkg/cache.Cache` methods** — the core domain interface called by the server handlers.
3. **Storage and chunk interfaces** — the pluggable backend contracts in `pkg/storage` and `pkg/storage/chunk`.

---

## Core Data Structure: `narinfo.NarInfo`

The central artifact type is `narinfo.NarInfo` from `github.com/nix-community/go-nix/pkg/narinfo`. All narinfo operations revolve around this struct:

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

---

## HTTP Endpoints

All endpoints are registered on a `chi.Mux` router in `pkg/server.Server.createRouter()`.

### Infrastructure Routes

| Method | Path | Handler | Notes |
|---|---|---|---|
| `GET` | `/healthz` | chi `middleware.Heartbeat` | Returns `200 OK` with body `"."`. Never traced. |
| `GET` | `/metrics` | `promhttp.HandlerFor` | Only registered when a Prometheus gatherer is configured. Never traced. |

### Nix Cache Protocol Routes

#### `GET /nix-cache-info`

Returns the Nix cache configuration text.

**Response:** `200 OK`, `Content-Type: text/plain`

```
StoreDir: /nix/store
WantMassQuery: 1
Priority: 30
```

#### `GET /pubkey`

Returns the cache's Ed25519 public key (used for narinfo signature verification by Nix clients).

**Response:** `200 OK`, `Content-Type: text/plain`, body is the base64-encoded public key.

---

#### `HEAD /{hash}.narinfo`

#### `GET /{hash}.narinfo`

Fetch narinfo metadata for a store path hash.

- `hash` must match `narinfo.HashPattern` (Nix base32).
- `HEAD` returns headers only (no body).
- `GET` returns full narinfo text.

**Response:** `200 OK`, `Content-Type: text/x-nix-narinfo`

The NAR URL in the returned narinfo is always normalized (any narinfo-hash prefix stripped).

**Failure modes:**
- `404 Not Found` — hash not in database, storage, or any upstream cache.
- `500 Internal Server Error` — database error or signature validation failure.

---

#### `PUT /upload/{hash}.narinfo`

Store a narinfo pushed by the client. Only available under `/upload` prefix.

**Request:** `Content-Type: text/x-nix-narinfo`, body is narinfo text.

**Response:** `200 OK` on success.

**Authorization:** `403 Forbidden` is evaluated by a strict configuration boolean — there is no auth middleware or token validation. `Server.SetPutPermitted(false)` (the default) causes the handler to immediately return `403` before any body is read. There is no per-request authentication.

**Failure modes:**
- `403 Forbidden` — PUT not permitted (`putPermitted == false`).
- `500 Internal Server Error` — parse or storage error.

---

#### `DELETE /{hash}.narinfo`

Delete a cached narinfo and its associated nar_file records.

**Response:** `200 OK` on success.

**Authorization:** Same as PUT — `deletePermitted` is a strict boolean set at startup via `Server.SetDeletePermitted`. No middleware or token evaluation occurs; a `false` value causes an immediate `403` response.

**Failure modes:**
- `403 Forbidden` — DELETE not permitted (`deletePermitted == false`).
- `404 Not Found` — hash not found.
- `500 Internal Server Error` — database or storage error.

---

#### `HEAD /nar/{hash}.nar`

#### `GET /nar/{hash}.nar`

#### `HEAD /nar/{hash}.nar.{compression}`

#### `GET /nar/{hash}.nar.{compression}`

Fetch a NAR archive. Supports uncompressed (`.nar`) and compressed (`.nar.xz`, `.nar.zst`, etc.) variants.

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

---

#### `PUT /upload/nar/{hash}.nar[.{compression}]`

Store a NAR pushed by the client.

**Request:** `Content-Type: application/x-nix-nar`, body is raw NAR bytes.

**Response:** `200 OK` on success.

**Failure modes:**
- `403 Forbidden` — PUT not permitted.
- `500 Internal Server Error` — storage error.

---

#### `DELETE /nar/{hash}.nar[.{compression}]`

Delete a cached NAR from storage and the database.

**Response:** `200 OK` on success.

**Failure modes:**
- `403 Forbidden` — DELETE not permitted.
- `404 Not Found`.
- `500 Internal Server Error`.

---

## `pkg/cache.Cache` — Core Methods

### NarInfo Operations

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

### NAR Operations

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

// HasNar checks if a NAR is available (whole file or chunks).
func (c *Cache) HasNar(ctx context.Context, narURL nar.URL) bool

// HasNarInChunks checks the database for CDC chunk records.
func (c *Cache) HasNarInChunks(ctx context.Context, narURL nar.URL) (bool, error)
```

### CDC Configuration

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

### Lifecycle

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

---

## `pkg/storage` — Storage Interfaces

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

---

## `pkg/storage/chunk` — Chunk Store Interface

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

Implementations: `chunk.NewLocalStore(baseDir)`, `chunk.NewS3Store(ctx, cfg, locker)`.

---

## `pkg/nar` — NAR URL Type

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

---

## Known Edge Cases and Failure Modes

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
