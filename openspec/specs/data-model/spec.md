# Data Model Specification

## Overview

ncps uses a relational database for all metadata. The database engine is selected at runtime via the `--cache-database-url` flag URL scheme. Schema is managed by **bun/migrate** and database access uses **Bun** query builder.

---

## Database Engines

| Engine | URL Scheme | Notes |
|---|---|---|
| SQLite | `sqlite:/path/to/db` | Default. `MaxOpenConns=1`, WAL mode, `busy_timeout=10s`, foreign keys ON |
| PostgreSQL | `postgresql://` or `postgres://` | Production. Configurable pool. |
| MySQL/MariaDB | `mysql://` or `mysql+unix://` | Production. `parseTime=true`, `loc=UTC`, `time_zone='+00:00'` forced. |

The `database.Open(dbURL, poolCfg)` function returns `*bun.DB` with the appropriate dialect. No `Querier` interface exists; all callers accept `*bun.DB` directly.

---

## Toolchain Conventions

### Bun Query Builder

- `github.com/uptrace/bun` is used for all database access.
- No code generation step — queries are written directly in Go using the Bun query builder.
- Engine-specific SQL (e.g., complex `ON CONFLICT … WHERE` clauses) is handled via `db.NewRaw(…)`.
- Column renames: `narinfo_id → NarInfoID`, `url → URL`.
- Type overrides:
  - `nar_files.file_size` → `uint64`
  - `chunks.size` → `uint32`
  - `chunks.compressed_size` → `uint32`
  - `nar_files.chunking_started_at` → `schema.NullTime` (nullable via `nullzero`)
- After modifying any query, edit the Go code directly in `pkg/database/`; no `sqlc generate` or `go generate` is needed.

### SQL Quoting Convention

**All column names must be quoted in every SQL query and schema definition, without exception.**

This is a blanket rule — it is not limited to reserved words or ambiguous identifiers. Every `SELECT`, `INSERT`, `UPDATE`, `DELETE`, and `CREATE TABLE` statement must quote every column name with double-quotes (e.g., `"id"`, `"hash"`, `"url"`, `"created_at"`). This prevents subtle, engine-specific reserved-word conflicts as the schema evolves and makes column references unambiguous across all three SQL dialects.

This rule applies to all migration files and raw SQL used in `pkg/database/`.

### bun/migrate Migrations

- Migration files live in `db/migrations/{sqlite,postgres,mysql}/` as `.up.sql` / `.down.sql` pairs.
- The version prefix format is `YYYYMMDDHHmmss_<name>`.
- The `ncps migrate` command (via `bun/migrate`) applies migrations; no external binary is required.
- **Never manually copy migration files.** Always use `ncps migrate new --engine <engine> --name <name>` to get correct timestamps.
- Applying migrations also regenerates `db/schema/{engine}.sql` snapshots.

---

## Schema

### `config`

Stores arbitrary key/value configuration (e.g., the secret signing key).

```sql
CREATE TABLE "config" (
    "id"         INTEGER PRIMARY KEY AUTOINCREMENT,
    "key"        TEXT NOT NULL UNIQUE,
    "value"      TEXT NOT NULL,
    "created_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at" TIMESTAMP
);
```

### `narinfos`

One row per cached narinfo. Narinfo fields are stored inline (denormalized) for fast lookup. The `url`, `compression`, and related fields are `NULL` for narinfos that exist in the database as stubs before their full metadata is populated.

```sql
CREATE TABLE "narinfos" (
    "id"               INTEGER PRIMARY KEY AUTOINCREMENT,
    "hash"             TEXT NOT NULL UNIQUE,
    "store_path"       TEXT,
    "url"              TEXT,
    "compression"      TEXT,
    "file_hash"        TEXT,
    "file_size"        BIGINT CHECK ("file_size" >= 0),
    "nar_hash"         TEXT,
    "nar_size"         BIGINT CHECK ("nar_size" >= 0),
    "deriver"          TEXT,
    "system"           TEXT,
    "ca"               TEXT,
    "created_at"       TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at"       TIMESTAMP,
    "last_accessed_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_narinfos_last_accessed_at ON "narinfos" ("last_accessed_at");
```

### `narinfo_references`

Stores the `References` field of a narinfo (one row per referenced store path).

```sql
CREATE TABLE "narinfo_references" (
    "narinfo_id" BIGINT NOT NULL REFERENCES "narinfos" ("id") ON DELETE CASCADE,
    "reference"  TEXT NOT NULL,
    PRIMARY KEY ("narinfo_id", "reference")
);
CREATE INDEX idx_narinfo_references_reference ON "narinfo_references" ("reference");
```

### `narinfo_signatures`

Stores the `Sig` lines of a narinfo (one row per signature).

```sql
CREATE TABLE "narinfo_signatures" (
    "narinfo_id" BIGINT NOT NULL REFERENCES "narinfos" ("id") ON DELETE CASCADE,
    "signature"  TEXT NOT NULL,
    PRIMARY KEY ("narinfo_id", "signature")
);
CREATE INDEX idx_narinfo_signatures_signature ON "narinfo_signatures" ("signature");
```

### `nar_files`

One row per unique (hash, compression, query) combination. Tracks both whole-file NARs and CDC-chunked NARs.

```sql
CREATE TABLE "nar_files" (
    "id"                  INTEGER PRIMARY KEY AUTOINCREMENT,
    "hash"                TEXT NOT NULL,
    "compression"         TEXT NOT NULL DEFAULT '',
    "file_size"           INTEGER NOT NULL,
    "query"               TEXT NOT NULL DEFAULT '',
    "total_chunks"        BIGINT NOT NULL DEFAULT 0,
    "chunking_started_at" TIMESTAMP NULL,
    "verified_at"         TIMESTAMP,
    "created_at"          TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at"          TIMESTAMP,
    "last_accessed_at"    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE ("hash", "compression", "query")
);
CREATE INDEX idx_nar_files_last_accessed_at ON "nar_files" ("last_accessed_at");
```

**CDC state machine for `total_chunks` / `chunking_started_at`:**

| `total_chunks` | `chunking_started_at` | Meaning |
|---|---|---|
| `0` | `NULL` | Not yet chunked; whole-file NAR in storage |
| `0` | `NOT NULL`, age < 1h | Chunking in progress (lock is fresh) |
| `0` | `NOT NULL`, age ≥ 1h | Stale lock; cleanup and restart chunking |
| `> 0` | any | Fully chunked; whole-file NAR deleted |

### `narinfo_nar_files`

Many-to-many join between narinfos and nar_files (multiple narinfos can reference the same physical NAR file).

```sql
CREATE TABLE "narinfo_nar_files" (
    "narinfo_id"  INTEGER NOT NULL REFERENCES "narinfos" ("id") ON DELETE CASCADE,
    "nar_file_id" INTEGER NOT NULL REFERENCES "nar_files" ("id") ON DELETE CASCADE,
    PRIMARY KEY ("narinfo_id", "nar_file_id")
);
CREATE INDEX idx_narinfo_nar_files_narinfo_id  ON "narinfo_nar_files" ("narinfo_id");
CREATE INDEX idx_narinfo_nar_files_nar_file_id ON "narinfo_nar_files" ("nar_file_id");
```

### `chunks`

One row per unique chunk content hash. Chunks are zstd-compressed on disk; `compressed_size` tracks the on-disk byte count.

```sql
CREATE TABLE "chunks" (
    "id"              INTEGER PRIMARY KEY AUTOINCREMENT,
    "hash"            TEXT NOT NULL UNIQUE,
    "size"            INTEGER NOT NULL CHECK ("size" >= 0),
    "compressed_size" INTEGER NOT NULL DEFAULT 0 CHECK ("compressed_size" >= 0),
    "created_at"      TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at"      TIMESTAMP
);
```

Chunk hash format: Nix base32, 52 characters.

### `nar_file_chunks`

Ordered sequence of chunks composing a NAR file.

```sql
CREATE TABLE "nar_file_chunks" (
    "nar_file_id"  INTEGER NOT NULL REFERENCES "nar_files" ("id") ON DELETE CASCADE,
    "chunk_id"     INTEGER NOT NULL REFERENCES "chunks" ("id") ON DELETE CASCADE,
    "chunk_index"  INTEGER NOT NULL,
    PRIMARY KEY ("nar_file_id", "chunk_index")
);
CREATE INDEX idx_nar_file_chunks_chunk_id ON "nar_file_chunks" ("chunk_id");
```

---

## Database Access Pattern

`pkg/cache` and other packages hold `*bun.DB` directly. All database operations are performed via functions in `pkg/database/` that accept `bun.IDB` (the interface implemented by both `*bun.DB` and `bun.Tx`), enabling transaction participation.

Key function groups in `pkg/database/` (non-exhaustive):

**Config:**
- `GetConfigByKey(ctx, db bun.IDB, key string) (Config, error)`
- `SetConfig(ctx, db bun.IDB, key, value string) error`
- `CreateConfig(ctx, db bun.IDB, key, value string) (Config, error)`

**NarInfo:**
- `GetNarInfoByHash(ctx, db bun.IDB, hash string) (NarInfo, error)`
- `GetNarInfoHashByNarURL(ctx, db bun.IDB, url string) (string, error)`
- `CreateNarInfo(ctx, db bun.IDB, arg CreateNarInfoParams) (NarInfo, error)`
- `TouchNarInfo(ctx, db bun.IDB, hash string) error`
- `GetLeastUsedNarInfos(ctx, db bun.IDB, limit int) ([]NarInfo, error)`
- `DeleteNarInfoByHash(ctx, db bun.IDB, hash string) error`
- `GetMigratedNarInfoHashes(ctx, db bun.IDB) ([]string, error)`

**NarFile:**
- `GetNarFileByHashAndCompressionAndQuery(ctx, db bun.IDB, hash, compression, query string) (NarFile, error)`
- `GetNarFileByNarInfoID(ctx, db bun.IDB, narInfoID int64) (NarFile, error)`
- `CreateNarFile(ctx, db bun.IDB, arg CreateNarFileParams) (NarFile, error)`
- `TouchNarFile(ctx, db bun.IDB, arg TouchNarFileParams) (int64, error)`
- `GetLeastUsedNarFiles(ctx, db bun.IDB, limit int) ([]NarFile, error)`
- `DeleteNarFileByID(ctx, db bun.IDB, id int64) (int64, error)`
- `GetOrphanedNarFiles(ctx, db bun.IDB) ([]NarFile, error)`

**Chunks (CDC):**
- `CreateChunk(ctx, db bun.IDB, arg CreateChunkParams) (Chunk, error)`
- `GetChunkByHash(ctx, db bun.IDB, hash string) (Chunk, error)`
- `CreateNarFileChunk(ctx, db bun.IDB, arg CreateNarFileChunkParams) error`
- `GetChunksByNarFileID(ctx, db bun.IDB, narFileID int64) ([]Chunk, error)`
- `GetOrphanedChunks(ctx, db bun.IDB) ([]Chunk, error)`
- `DeleteChunkByID(ctx, db bun.IDB, id int64) error`
- `DeleteNarFileChunksByNarFileID(ctx, db bun.IDB, narFileID int64) error`

**Transactions:**
- Use `db.RunInTx(ctx, opts, func(ctx context.Context, tx bun.Tx) error)` directly on `*bun.DB`

---

## Entity Relationship Summary

```
config
  key (PK)

narinfos ──────────────── narinfo_references   (1:N, cascade delete)
         ──────────────── narinfo_signatures    (1:N, cascade delete)
         ──┐
           │ narinfo_nar_files (M:N join)
           └──────────── nar_files ──────────── nar_file_chunks (1:N, cascade delete)
                                                      │
                                                   chunks (N:1, cascade delete)
```

All foreign keys use `ON DELETE CASCADE` so that removing a `narinfo` or `nar_file` record automatically cleans up child rows.
