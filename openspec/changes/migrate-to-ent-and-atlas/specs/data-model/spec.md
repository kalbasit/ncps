# Data Model Specification (Delta)

## MODIFIED Requirements

### Requirement: Engine selection by URL scheme

The system SHALL select the database engine at runtime from the scheme of the `--cache-database-url` flag. The function `database.Open(dbURL, poolCfg)` SHALL return a `*database.Client` (an Ent client paired with driver metadata) configured with the appropriate dialect and underlying SQL driver.

#### Scenario: Selecting SQLite

- **WHEN** `--cache-database-url=sqlite:/path/to/db` is passed
- **THEN** `database.Open` SHALL return a `*database.Client` configured with `dialect.SQLite` and the `mattn/go-sqlite3` driver
- **AND** the SQLite driver SHALL be configured with `MaxOpenConns=1`, WAL mode, `busy_timeout=10000ms`, and `foreign_keys=ON`

#### Scenario: Selecting PostgreSQL

- **WHEN** `--cache-database-url=postgresql://...` or `--cache-database-url=postgres://...` is passed
- **THEN** `database.Open` SHALL return a `*database.Client` configured with `dialect.Postgres` and the `jackc/pgx/v5/stdlib` driver
- **AND** the PostgreSQL driver pool SHALL be configurable via `PoolConfig`

#### Scenario: Selecting MySQL/MariaDB

- **WHEN** `--cache-database-url=mysql://...` or `--cache-database-url=mysql+unix://...` is passed
- **THEN** `database.Open` SHALL return a `*database.Client` configured with `dialect.MySQL` and the `go-sql-driver/mysql` driver
- **AND** the MySQL driver SHALL be configured with `parseTime=true`, `loc=UTC`, and the connection SHALL force `time_zone='+00:00'`

#### Scenario: Unsupported scheme

- **WHEN** a URL whose scheme is not one of the above is passed
- **THEN** `database.Open` SHALL return an error of type `database.ErrUnsupportedDriver`

## ADDED Requirements

### Requirement: Tables, columns, indexes, and CHECKs are defined as Ent schemas

The system SHALL define every table described by this capability — `config`, `narinfos`, `narinfo_references`, `narinfo_signatures`, `nar_files`, `narinfo_nar_files`, `chunks`, `nar_file_chunks` — as an Ent schema under `ent/schema/<entity>.go`. The Ent schema SHALL be the single source of truth for column names, types, nullability, defaults, indexes, foreign keys, and CHECK constraints. The SQL `CREATE TABLE` shapes documented elsewhere in this specification SHALL exactly match what Ent's generator emits for each supported dialect.

#### Scenario: Schema parity at apply time

- **WHEN** all translated migrations are applied to a fresh database (per dialect) and `atlas migrate diff` is run against the Ent schema definitions
- **THEN** the diff SHALL be empty for SQLite, PostgreSQL, and MySQL — proving the Ent schemas reproduce the documented shape exactly

#### Scenario: Adding a new column

- **WHEN** a developer needs to add a column to an existing table
- **THEN** they SHALL edit the corresponding `ent/schema/<entity>.go` file
- **AND** they SHALL NOT hand-edit any SQL DDL, generated client code, or migration file

## REMOVED Requirements

### Requirement: sqlc generates per-engine Querier interfaces

**Reason**: sqlc is replaced by Ent. Per-engine Querier interfaces (`sqlitedb.Querier`, `postgresdb.Querier`, `mysqldb.Querier`) and the `sqlc.yaml` configuration are removed in favour of a single Ent client whose dialect is selected at runtime from the URL scheme.

**Migration**: Delete `sqlc.yaml`. Delete `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, `pkg/database/mysqldb/`. The Ent client (`*ent.Client`) returned by `database.Open` replaces the per-engine Querier surface. Call sites are converted to the Ent fluent API.

### Requirement: Hand-written `database.Querier` superset interface

**Reason**: The hand-written `database.Querier` superset interface and the engine-specific wrappers (`sqliteWrapper`, `postgresWrapper`, `mysqlWrapper`) become redundant once the Ent client is the only database surface.

**Migration**: Delete `pkg/database/generated_querier.go`, `pkg/database/generated_models.go`, `pkg/database/generated_errors.go`, and `pkg/database/generated_wrapper_{sqlite,postgres,mysql}.go`. Replace `database.Querier` parameters in `pkg/cache/`, `pkg/ncps/`, `pkg/server/`, and `cmd/` with `*database.Client` or `*ent.Tx` parameters per the database-orm capability.

### Requirement: sqlc type and column overrides

**Reason**: Type overrides previously declared in `sqlc.yaml` (`nar_files.file_size → uint64`, `chunks.size → uint32`, `chunks.compressed_size → uint32`, `nar_files.chunking_started_at → database/sql.NullTime`) and column renames (`narinfo_id → NarInfoID`, `url → URL`) are reapplied as Ent field configurations via `GoType(...)` or via the Ent field-naming defaults.

**Migration**: Apply each type override on the corresponding Ent field declaration in `ent/schema/<entity>.go`. Ent's default field naming already produces `NarInfoID` and `URL` for the snake_case columns `narinfo_id` and `url`, so no explicit rename is required.

### Requirement: sqlc regeneration workflow

**Reason**: The `sqlc generate` + `go generate ./pkg/database` workflow is replaced by `task ent:generate` (or `go generate ./ent/...` directly), which runs `go tool ent generate` to refresh the committed Ent client.

**Migration**: Update `CLAUDE.md`, `.agent/skills/`, and developer documentation to describe the new workflow. Remove the `//go:generate go tool sqlc-multi-db ...` directive from `pkg/database/`. Remove `github.com/kalbasit/sqlc-multi-db` from `go.mod`.

### Requirement: All SQL column names are quoted

**Reason**: The blanket rule on application-level SQL no longer applies because application code no longer writes SQL. Ent's generator emits per-dialect identifier quoting automatically. The rule survives only in migration files and SQL-only stubs, where it follows each dialect's native conventions rather than a uniform double-quote policy.

**Migration**: No code change required. The rule is removed from `CLAUDE.md` and replaced with a note that Atlas controls quoting in generated migrations and that SQL-only stubs follow per-dialect conventions.

### Requirement: dbmate manages versioned migrations

**Reason**: dbmate is replaced by Atlas (library-based migration generation) and Goose (runtime applier). See the database-migrations capability for the new requirements and the dbmate → Goose adoption procedure.

**Migration**: `db/migrations/{sqlite,postgres,mysql}/*` are translated mechanically to `migrations/<dialect>/*` with preserved timestamps and Goose-format header directives. The `nix/dbmate-wrapper/` is deleted, and the `dbmate` binary is removed from the dev shell and Docker images. Operators upgrade through the automatic in-place adoption defined in the database-migrations capability.

### Requirement: dbmate `new` is the only way to create migration files

**Reason**: Replaced by `task migrations:gen NAME=<descriptive_name>` (schema-driven) and `task migrations:sql NAME=<descriptive_name>` (SQL-only stub) per the database-migrations capability. The descriptive-`NAME` rejection of placeholder names (e.g. `auto`, `wip`, `tmp`) is preserved and tightened.

**Migration**: Update `.agent/skills/migrate-new/SKILL.md` to drive the task-based workflow.

### Requirement: Applying migrations regenerates the schema snapshots

**Reason**: dbmate's `db/schema/<engine>.sql` snapshots are replaced by the Ent schema (`ent/schema/*.go`) as the source-of-truth representation. The current Atlas-generated migration files are the canonical applied state.

**Migration**: Delete `db/schema/sqlite.sql`, `db/schema/postgres.sql`, and `db/schema/mysql.sql`. The schema-equivalence golden test (database-migrations capability) replaces the snapshots as the drift detector.

### Requirement: `database.Querier` exposes config CRUD

**Reason**: The `database.Querier` method-list requirements are replaced by the Ent fluent API documented in the database-orm capability. CRUD on the `config` table is now expressed as `client.Config.Query()...Only(ctx)`, `client.Config.Create()...Save(ctx)`, etc.

**Migration**: Convert every call site that previously invoked `GetConfigByKey` / `SetConfig` / `CreateConfig` to the equivalent Ent fluent API expression. Error handling continues to distinguish "not found" via the Ent-specific `ent.IsNotFound(err)` predicate (or wrapped per project convention).

### Requirement: `database.Querier` exposes narinfo CRUD

**Reason**: Replaced by the Ent fluent API on `*ent.NarInfoClient` and the corresponding query/predicate packages. See the database-orm capability for the surface contract.

**Migration**: Convert every `GetNarInfoByHash` / `GetNarInfoHashByNarURL` / `CreateNarInfo` / `UpdateNarInfoLastAccessedAt` / `GetLeastUsedNarInfos` / `DeleteNarInfoByHash` / `GetMigratedNarInfoHashes` call site to the equivalent Ent fluent expression. `GetMigratedNarInfoHashes` becomes `client.NarInfo.Query().Where(narinfo.URLNotNil()).Select(narinfo.FieldHash).Strings(ctx)`.

### Requirement: `database.Querier` exposes nar_file CRUD

**Reason**: Replaced by the Ent fluent API on `*ent.NarFileClient`. See the database-orm capability.

**Migration**: Convert every `GetNarFileByHashAndCompressionAndQuery` / `GetNarFileByNarInfoID` / `CreateNarFile` / `UpdateNarFileLastAccessedAt` / `GetLeastUsedNarFiles` / `DeleteNarFileByID` / `GetOrphanedNarFiles` call site to the equivalent Ent fluent expression. `GetOrphanedNarFiles` becomes a query whose predicate filters nar_files for which no `narinfo_nar_files` edge exists.

### Requirement: `database.Querier` exposes chunk CRUD

**Reason**: Replaced by the Ent fluent API on `*ent.ChunkClient` and `*ent.NarFileChunkClient`. See the database-orm capability.

**Migration**: Convert every `CreateChunk` / `GetChunkByHash` / `CreateNarFileChunk` / `GetChunksByNarFileID` / `GetOrphanedChunks` / `DeleteChunkByID` / `DeleteNarFileChunksByNarFileID` call site to the equivalent Ent fluent expression. Ordered chunk retrieval uses `Order(ent.Asc(narfilechunk.FieldChunkIndex))` to preserve the existing index-ordered semantics.

### Requirement: `database.Querier` supports transactions

**Reason**: Replaced by Ent's transaction surface (`*ent.Tx`). The cache layer's `withTransaction(name, fn)` wrapper is preserved with its closure parameter reshaped to `func(*ent.Tx) error`.

**Migration**: Convert every call site that previously used `WithTransaction(ctx, fn)` on the Querier to `c.withTransaction(ctx, "<span_name>", func(tx *ent.Tx) error { ... })` against the cache's transaction helper, which calls `client.Tx(ctx)` internally and handles commit/rollback per the closure's return value.
