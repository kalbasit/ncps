## MODIFIED Requirements

### Requirement: `narinfos` table schema

The system SHALL maintain a `narinfos` table with one row per cached narinfo, storing the narinfo fields denormalized inline for fast lookup. The `url`, `compression`, and related fields MAY be `NULL` for narinfo stubs whose full metadata has not yet been populated. The `upstream_url` column MAY be `NULL` and SHALL hold the original opaque upstream NAR path (the path before `.nar`, e.g. `nar/<uuid>.nar.zst`) when the upstream narinfo URL was not hash-named; it stays `NULL` for conventional hash-named upstreams.

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
    "upstream_url"     TEXT,
    "created_at"       TIMESTAMP DEFAULT CURRENT_TIMESTAMP NOT NULL,
    "updated_at"       TIMESTAMP,
    "last_accessed_at" TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_narinfos_last_accessed_at ON "narinfos" ("last_accessed_at");
```

#### Scenario: Inserting a complete narinfo

- **WHEN** application code inserts a narinfo with all fields populated
- **THEN** the row SHALL be created and `last_accessed_at` SHALL default to the current timestamp

#### Scenario: Stub narinfo

- **WHEN** a narinfo row exists with only `hash` populated and the rest NULL
- **THEN** the database SHALL accept the row (no NOT NULL constraint on the optional fields)

#### Scenario: CHECK violation on negative file_size

- **WHEN** an insert attempts `file_size = -1`
- **THEN** the database SHALL reject the row via the CHECK constraint

#### Scenario: Recording an opaque upstream URL

- **WHEN** a narinfo is cached from an upstream whose NAR URL was opaque (not hash-named)
- **THEN** the `upstream_url` column SHALL hold the original opaque upstream path
- **AND** for a conventional hash-named upstream the `upstream_url` column SHALL remain NULL
