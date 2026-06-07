## MODIFIED Requirements

### Requirement: Cache NAR Operations

The `pkg/cache.Cache` type SHALL expose the core NAR domain methods called by the server handlers.

```go
// GetNar resolves a NAR and returns the URL it is served under, its size, a
// reader for the bytes, and an error.
// Lookup order (CDC disabled): storage → upstream.
// Lookup order (CDC enabled): chunks (DB) → storage → upstream.
// An upstream fetch: downloads to temp file, atomically moves to storage,
// writes DB record, optionally triggers CDC chunking.
// The returned size is the concrete nar_files.file_size when served from store
// or chunks, and -1 (size unknown) when streaming a download still in flight.
func (c *Cache) GetNar(ctx context.Context, narURL nar.URL) (nar.URL, int64, io.ReadCloser, error)

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

### Requirement: Storage Interfaces

The `pkg/storage` package SHALL define the pluggable backend contracts implemented by the filesystem and S3 backends.

```go
// NarInfoStore — narinfo metadata storage (filesystem or S3).
type NarInfoStore interface {
    HasNarInfo(ctx context.Context, hash string) bool
    GetNarInfo(ctx context.Context, hash string) (*narinfo.NarInfo, error)
    PutNarInfo(ctx context.Context, hash string, narInfo *narinfo.NarInfo) error
    DeleteNarInfo(ctx context.Context, hash string) error
    WalkNarInfos(ctx context.Context, fn func(hash string) error) error
}

// NarStore — NAR archive storage (filesystem or S3).
type NarStore interface {
    HasNar(ctx context.Context, narURL nar.URL) bool
    // StatNar distinguishes a confirmed absence (false, nil) from an
    // undeterminable result (false, err); callers MUST NOT treat (false, err)
    // as a confirmed absence.
    StatNar(ctx context.Context, narURL nar.URL) (bool, error)
    // GetNar returns the size and a reader for the NAR. The caller MUST close
    // the returned io.ReadCloser.
    GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error)
    // PutNar stores the NAR; size > 0 is the known size, size <= 0 means
    // unknown (e.g. on-the-fly re-compression). Returns the stored size.
    PutNar(ctx context.Context, narURL nar.URL, body io.Reader, size int64) (int64, error)
    DeleteNar(ctx context.Context, narURL nar.URL) error
    WalkNars(ctx context.Context, fn func(narURL nar.URL) error) error
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
