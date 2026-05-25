## ADDED Requirements

### Requirement: `build_trace_entries` table schema
The system SHALL maintain a `build_trace_entries` table with one row per stored build trace entry. The `(drv_path, output_name)` pair SHALL be unique. The `raw_json` column SHALL store the verbatim upload body as a forward-compatibility safety valve.

```sql
CREATE TABLE "build_trace_entries" (
    "id"          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    "drv_path"    TEXT         NOT NULL,
    "output_name" VARCHAR(255) NOT NULL,
    "out_path"    TEXT         NOT NULL,
    "raw_json"    TEXT         NOT NULL,
    "created_at"  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at"  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX "idx_build_trace_entries_drv_path_output_name"
    ON "build_trace_entries" ("drv_path", "output_name");
```

#### Scenario: Inserting a build trace entry
- **WHEN** application code inserts a row with `drv_path`, `output_name`, `out_path`, and `raw_json` all populated
- **THEN** the row SHALL be persisted and retrievable by `(drv_path, output_name)`

#### Scenario: Unique constraint on (drv_path, output_name)
- **WHEN** application code attempts to insert a second row with the same `(drv_path, output_name)`
- **THEN** the database SHALL reject the insert with a unique constraint violation (the application layer handles this as an upsert)

### Requirement: `build_trace_signatures` table schema
The system SHALL maintain a `build_trace_signatures` table storing one row per signature per build trace entry, with a FOREIGN KEY to `build_trace_entries` with `ON DELETE CASCADE`. Note: the Ent ORM maps `field.String()` to `varchar(255)` in MySQL; `key_name` and `signature` use this type in the MySQL migration. Ed25519 signatures base64-encoded are ~88 characters, well within this limit.

```sql
CREATE TABLE "build_trace_signatures" (
    "id"                   BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
    "build_trace_entry_id" BIGINT       NOT NULL REFERENCES "build_trace_entries" ("id") ON DELETE CASCADE,
    "key_name"             VARCHAR(255) NOT NULL,
    "signature"            TEXT         NOT NULL
);
CREATE INDEX "idx_build_trace_signatures_entry_id"
    ON "build_trace_signatures" ("build_trace_entry_id");
```

#### Scenario: Inserting signatures for an entry
- **WHEN** a build trace entry is stored with two signatures (one upstream, one from ncps)
- **THEN** two rows SHALL exist in `build_trace_signatures` linked to the entry's `id`

#### Scenario: Cascade delete
- **WHEN** a `build_trace_entries` row is deleted
- **THEN** all associated `build_trace_signatures` rows SHALL be deleted automatically

### Requirement: Ent schema coverage for build trace tables
The system SHALL define `BuildTraceEntry` and `BuildTraceSignature` as Ent schemas under `ent/schema/`. These SHALL be the single source of truth for the table shapes above, consistent with the existing invariant that all tables are defined as Ent schemas.

#### Scenario: Schema parity at apply time
- **WHEN** all migrations are applied to a fresh database (per dialect) and `atlas migrate diff` is run against the Ent schema definitions
- **THEN** the diff SHALL be empty for SQLite, PostgreSQL, and MySQL
