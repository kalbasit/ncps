## MODIFIED Requirements

### Requirement: Database Engines
The `database.Open(dbURL, poolCfg)` function SHALL return `*bun.DB` (not a `database.Querier` interface). The engine is selected from the URL scheme as before. The three engine-specific sub-packages (`sqlitedb`, `postgresdb`, `mysqldb`) SHALL no longer exist; Bun handles all engines from a single implementation.

| Engine | URL Scheme | Notes |
|---|---|---|
| SQLite | `sqlite:/path/to/db` | Default. `MaxOpenConns=1`, WAL mode, `busy_timeout=10s`, foreign keys ON |
| PostgreSQL | `postgresql://` or `postgres://` | Production. Configurable pool. |
| MySQL/MariaDB | `mysql://` or `mysql+unix://` | Production. `parseTime=true`, `loc=UTC`, `time_zone='+00:00'` forced. |

#### Scenario: Open returns *bun.DB
- **WHEN** `database.Open(url, cfg)` is called with any supported URL scheme
- **THEN** a configured `*bun.DB` is returned (not a `Querier` interface)

#### Scenario: Engine sub-packages removed
- **WHEN** the codebase is inspected
- **THEN** `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, and `pkg/database/mysqldb/` do not exist

---

### Requirement: Toolchain — Bun replaces sqlc and dbmate
Database access SHALL use `github.com/uptrace/bun`. sqlc, sqlc-multi-db, and dbmate SHALL be removed from the toolchain. Migration files are embedded in the binary via `go:embed`.

#### Scenario: No sqlc generation step
- **WHEN** a developer modifies a database query
- **THEN** they edit Go code directly in `pkg/database/`; no `sqlc generate` or `go generate` command is needed

#### Scenario: No dbmate binary needed
- **WHEN** a developer or operator wants to run migrations
- **THEN** they run `ncps migrate up`; no external `dbmate` binary is required

---

### Requirement: Toolchain — SQL quoting convention unchanged
All column names MUST be quoted in every SQL query and schema definition, without exception. This rule applies to all migration files and any raw SQL used in `pkg/database/`.

#### Scenario: Column names quoted in raw SQL
- **WHEN** a raw SQL query is written in `pkg/database/`
- **THEN** every column reference is wrapped in double quotes (e.g. `"id"`, `"hash"`, `"created_at"`)

---

### Requirement: Migration files follow bun/migrate convention
Migration files SHALL live in `db/migrations/{sqlite,postgres,mysql}/` as `.up.sql` / `.down.sql` pairs with `YYYYMMDDHHmmss_<name>` timestamp prefixes. The `-- migrate:up` / `-- migrate:down` single-file format SHALL no longer be used.

#### Scenario: Migration file format
- **WHEN** a migration directory is listed
- **THEN** each migration appears as two files: `<timestamp>_<name>.up.sql` and `<timestamp>_<name>.down.sql`

---

### Requirement: Model structs have complete bun tags
All database model structs (`NarInfo`, `NarFile`, `Chunk`, `Config`, `PinnedClosure`, and junction structs) SHALL carry explicit `bun` struct tags on every field. The `database.Querier` interface and all generated parameter structs (`CreateNarInfoParams`, etc.) SHALL be removed.

#### Scenario: NarInfo struct tags
- **WHEN** the `NarInfo` struct is inspected
- **THEN** every field has an explicit `bun:"<column>"` tag; nullable fields additionally carry `nullzero`

#### Scenario: No generated parameter structs
- **WHEN** the codebase is inspected
- **THEN** structs such as `CreateNarInfoParams`, `TouchNarFileParams`, etc., do not exist; callers use the Bun query builder directly

## REMOVED Requirements

### Requirement: sqlc Toolchain
**Reason**: Replaced by `github.com/uptrace/bun`. sqlc generates three separate engine-specific packages that must be kept in sync; Bun handles all engines from a single implementation without code generation.
**Migration**: Delete `sqlc.yaml`, `db/query.*.sql`, and all `pkg/database/generated_*.go` files. Edit `pkg/database/` directly for query changes.

### Requirement: dbmate Migrations
**Reason**: Replaced by `bun/migrate` with embedded SQL files. The external `dbmate` binary and its nix wrapper add installation complexity and require out-of-band tooling.
**Migration**: Rename existing migration files to the `.up.sql` / `.down.sql` convention and run `ncps migrate up` instead of `dbmate up`.
