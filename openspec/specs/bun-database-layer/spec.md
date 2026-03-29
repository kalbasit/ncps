# Bun Database Layer Specification

## Overview

This spec defines the database access layer using `github.com/uptrace/bun` as the sole database access mechanism, replacing the previous sqlc+dbmate approach.

---

## `*bun.DB` is the sole database handle

`pkg/database.Open()` SHALL return `*bun.DB`. No custom `Querier` interface SHALL exist. All callers that previously accepted `database.Querier` SHALL accept `*bun.DB` directly.

#### Scenario: Opening a SQLite database
- **WHEN** `database.Open("sqlite:/path/to/db.sqlite", nil)` is called
- **THEN** a `*bun.DB` configured with the SQLite dialect, WAL mode, foreign keys ON, and `busy_timeout=10s` is returned

#### Scenario: Opening a PostgreSQL database
- **WHEN** `database.Open("postgresql://user:pass@host/db", cfg)` is called
- **THEN** a `*bun.DB` configured with the PostgreSQL dialect and the provided pool settings is returned

#### Scenario: Opening a MySQL database
- **WHEN** `database.Open("mysql://user:pass@host/db", nil)` is called
- **THEN** a `*bun.DB` configured with the MySQL dialect, `parseTime=true`, `loc=UTC`, and `time_zone='+00:00'` is returned

#### Scenario: Unsupported URL scheme
- **WHEN** `database.Open("redis://localhost/0", nil)` is called
- **THEN** an `ErrUnsupportedDriver` error is returned

---

## OTel instrumentation is preserved

`database.Open()` SHALL wrap the underlying `*sql.DB` with `otelsql` before passing it to Bun, so that all database calls emit OpenTelemetry traces and metrics exactly as before.

#### Scenario: SQLite spans
- **WHEN** a query is executed via the returned `*bun.DB`
- **THEN** an OTel span with `db.system = sqlite` is created

#### Scenario: PostgreSQL spans
- **WHEN** a query is executed via the returned `*bun.DB` for a PostgreSQL connection
- **THEN** an OTel span with `db.system = postgresql` is created

---

## Bun model structs have complete struct tags

Every field on every model struct (`NarInfo`, `NarFile`, `Chunk`, `Config`, `PinnedClosure`, and junction structs) SHALL carry a `bun` struct tag explicitly naming the column. No field SHALL rely on Bun's automatic snake_case inference.

#### Scenario: All fields tagged
- **WHEN** any model struct is inspected
- **THEN** every field (including `bun.BaseModel`) has an explicit `bun:"<column_name>"` tag

#### Scenario: Nullable fields use nullzero
- **WHEN** a model field is nullable (`sql.NullString`, `sql.NullInt64`, `sql.NullTime`)
- **THEN** its tag includes `nullzero` so that zero values are stored as SQL NULL

---

## Transactions use `bun.IDB`

Code that must execute multiple statements atomically SHALL use `db.RunInTx(ctx, opts, func(ctx, tx bun.Tx) error)` or accept `bun.IDB` as an argument so it can participate in a caller-provided transaction.

#### Scenario: Successful transaction
- **WHEN** `db.RunInTx` is called with a function that executes multiple writes
- **THEN** all writes are committed atomically if the function returns nil

#### Scenario: Transaction rollback on error
- **WHEN** `db.RunInTx` is called and the inner function returns a non-nil error
- **THEN** all writes within the transaction are rolled back

---

## Engine-specific SQL via Bun query builder or raw queries

All database operations SHALL use the Bun query builder. Where the query builder cannot express engine-specific SQL (e.g. complex `ON CONFLICT … WHERE` clauses), raw SQL via `db.NewRaw(…)` is acceptable. Engine differences SHALL be handled within `pkg/database` and SHALL NOT leak to callers.

#### Scenario: Bulk insert across engines
- **WHEN** multiple rows are inserted in a single call (e.g. adding multiple references)
- **THEN** the correct engine-specific syntax is used (Bun normalises `VALUES` batches automatically)

#### Scenario: ON CONFLICT upsert
- **WHEN** a record is inserted that conflicts with an existing unique key
- **THEN** the Bun `On("CONFLICT … DO UPDATE SET …")` clause resolves the conflict correctly on all three engines

---

## Engine sub-packages are removed

The `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, and `pkg/database/mysqldb/` sub-packages SHALL be deleted. No generated wrapper or adapter code SHALL exist.

#### Scenario: Clean package structure
- **WHEN** the repository is inspected after the migration
- **THEN** `pkg/database/` contains only hand-written Go files; no `sqlitedb/`, `postgresdb/`, or `mysqldb/` sub-directories exist
