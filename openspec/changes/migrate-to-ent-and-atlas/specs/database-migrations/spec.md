# Database Migrations Specification

## ADDED Requirements

### Requirement: Per-dialect versioned SQL migrations live under `migrations/<dialect>/`

The system SHALL maintain one migration directory per supported dialect at `migrations/sqlite/`, `migrations/postgres/`, and `migrations/mysql/`. Each directory SHALL contain timestamp-prefixed `.sql` files in Goose format (`-- +goose Up` / `-- +goose Down` markers) and an `atlas.sum` integrity file maintained by Atlas. The directory tree SHALL be embedded into the binary via `//go:embed` in `migrations/migrations.go`.

#### Scenario: Embedding the migrations

- **WHEN** the binary is built
- **THEN** `migrations.MigrationsFS` SHALL embed every file under `migrations/sqlite/`, `migrations/postgres/`, and `migrations/mysql/`
- **AND** at runtime, `fs.Sub(MigrationsFS, dialect)` SHALL yield the dialect-specific sub-FS that Goose can consume

#### Scenario: Integrity verification

- **WHEN** any file in `migrations/<dialect>/` is modified without the corresponding `atlas.sum` update
- **THEN** the CI `atlas-sum-check` step SHALL fail with a non-zero exit

### Requirement: Migrations are generated from Ent schemas via Atlas as a Go library

The system SHALL provide a `cmd/generate-migrations` Go program that imports `ariga.io/atlas/sql/sqltool` and `entgo.io/ent/dialect/sql/schema` to diff the current Ent schema against the latest applied migration state and emit a new Goose-formatted `.sql` file per dialect in a single invocation. Atlas SHALL be consumed only as a Go library — no `atlas` CLI binary SHALL be required to be installed.

#### Scenario: Generating a schema-driven migration

- **WHEN** a developer edits an Ent schema and runs `task migrations:gen NAME=add_widget_count`
- **THEN** `cmd/generate-migrations` SHALL produce three new files — `migrations/sqlite/<ts>_add_widget_count.sql`, `migrations/postgres/<ts>_add_widget_count.sql`, `migrations/mysql/<ts>_add_widget_count.sql` — sharing one timestamp prefix
- **AND** each dialect's `atlas.sum` SHALL be updated accordingly

#### Scenario: Generating a SQL-only stub for a backfill

- **WHEN** a developer runs `task migrations:sql NAME=backfill_orphan_chunks`
- **THEN** `cmd/generate-migrations --sql-only --name backfill_orphan_chunks` SHALL produce three empty Goose stubs (`-- +goose Up\n\n-- +goose Down\n`), one per dialect, with a shared timestamp prefix

#### Scenario: Rejecting placeholder names

- **WHEN** a developer runs `cmd/generate-migrations` with `--name=auto`, `--name=wip`, `--name=tmp`, or an empty/whitespace-only name
- **THEN** the command SHALL exit non-zero with a diagnostic listing the rejected name

### Requirement: Migrations are applied at runtime by Goose against the `schema_migrations` table

The system SHALL provide an `ncps migrate up` command that opens the configured database, selects the Goose dialect from the URL scheme, mounts `migrations/<dialect>/` via `fs.Sub`, and calls `goose.NewProvider(dialect, db, subFS, goose.WithTableName("schema_migrations")).Up(ctx)`. Goose's tracking table SHALL be named `schema_migrations` (preserving the dbmate-era name for operator continuity) and SHALL use Goose's canonical 4-column schema (`id`, `version_id`, `is_applied`, `tstamp`) after adoption.

#### Scenario: Fresh install

- **WHEN** `ncps migrate up` is run against a database where `schema_migrations` does not exist
- **THEN** Goose SHALL create `schema_migrations` with its canonical schema (using the `schema_migrations` name)
- **AND** all translated migrations SHALL be applied in timestamp order
- **AND** the final state SHALL have `schema_migrations.is_applied=TRUE` rows for every applied migration

#### Scenario: Already-adopted install

- **WHEN** `ncps migrate up` is run against a database where `schema_migrations` exists with goose's canonical schema (i.e. `is_applied` column present) and all migrations are already applied
- **THEN** the command SHALL be a no-op against the schema and SHALL exit zero

### Requirement: Existing dbmate-format `schema_migrations` is adopted in-place at first `migrate up`

The system SHALL detect a dbmate-format `schema_migrations` table (a `version` column without `is_applied`) on the first invocation of `migrate up` and SHALL convert it to Goose's canonical schema. Adoption SHALL preserve all historical migration version records. The legacy `version` column SHALL be removed from the final table — no half-populated columns SHALL remain visible to operators.

#### Scenario: SQLite + Postgres adoption is transactional

- **WHEN** `migrate up` runs against a SQLite or Postgres database where `schema_migrations` has the dbmate shape
- **THEN** the entire conversion (create temp table, drop old table, create new table with Goose schema, insert rows, verify row count) SHALL execute inside a single transaction
- **AND** if the row-count verify fails, the transaction SHALL roll back and the database SHALL retain its dbmate shape unchanged

#### Scenario: MySQL adoption uses backup-table state machine

- **WHEN** `migrate up` runs against a MySQL/MariaDB database where `schema_migrations` has the dbmate shape
- **THEN** the command SHALL rename `schema_migrations` to `schema_migrations_dbmate_backup`, create a new `schema_migrations` with Goose's canonical schema, insert rows from the backup, verify row-count parity, and drop the backup
- **AND** if any step fails or the process crashes mid-sequence, the next `migrate up` SHALL detect the partial state (table existence + column presence probe) and resume from the correct point per the state machine defined in `design.md` (states S3–S5)

#### Scenario: Adoption is idempotent

- **WHEN** `migrate up` is run twice in succession against the same database
- **THEN** the second invocation SHALL detect that adoption is already complete (or that no adoption is needed) and SHALL exit zero without issuing any DDL

#### Scenario: Partial dbmate history is honoured

- **WHEN** `migrate up` runs against a database whose dbmate `schema_migrations` table records only a *subset* of the dbmate migration history (e.g. a v0.4-era SQLite install with only early migrations applied)
- **THEN** adoption SHALL preserve all recorded versions in the converted table
- **AND** Goose SHALL apply only the translated migrations whose versions are *not* recorded — historical DDL SHALL NOT re-execute

### Requirement: `migrate up` exposes a `--dry-run` flag

The system SHALL accept a `--dry-run` flag on `ncps migrate up` that prints (a) the detected adoption state, (b) the list of migration versions that would be applied, and (c) whether `migrate up` would adopt the tracking table — without issuing any DDL or modifying any data.

#### Scenario: Inspecting a dbmate install

- **WHEN** an operator runs `ncps migrate up --dry-run` against a dbmate-format database
- **THEN** the output SHALL include "adoption needed: yes" and the dialect-specific adoption strategy that would run
- **AND** no rows in `schema_migrations` SHALL be modified, and no DDL SHALL be executed

### Requirement: Down migrations are not supported

The system SHALL NOT support `ncps migrate down`. The `migrate` subcommand SHALL document this and SHALL exit non-zero with an explanatory message pointing operators at the expand-contract recipe and the four-step NOT NULL promotion procedure when `migrate down` is invoked.

#### Scenario: Invoking down

- **WHEN** an operator runs `ncps migrate down`
- **THEN** the command SHALL exit non-zero
- **AND** the error output SHALL reference the expand-contract recipe and the four-step NOT NULL recipe documented in `CLAUDE.md`

### Requirement: Migrations follow the expand-contract policy

The system SHALL produce migrations that are safe to apply while the immediately preceding application version is still serving traffic. Forbidden DDL in a single migration file SHALL include: `DROP COLUMN`, `DROP TABLE`, `RENAME COLUMN`, `RENAME TABLE`, and adding `NOT NULL` to an existing nullable column that may have populated rows. Permitted operations include adding nullable columns, adding tables, adding indexes, adding new Postgres `ENUM` values, and adding `NOT NULL` columns to newly-created (empty) tables.

#### Scenario: Forbidden DDL in the newest migration

- **WHEN** the newest file in any `migrations/<dialect>/` directory contains a forbidden DDL statement
- **THEN** `cmd/ent-lint` SHALL fail with a non-zero exit and a checklist-formatted message naming the file and the forbidden statement

#### Scenario: Promoting a column to NOT NULL safely

- **WHEN** a developer needs to promote an existing nullable column to NOT NULL
- **THEN** they SHALL follow the four-step recipe documented in `CLAUDE.md`: (1) `task migrations:gen NAME=add_<col>_nullable` adds the column as `Optional()`; (2) application code is updated to always set the column; (3) `task migrations:sql NAME=backfill_<col>` produces a SQL-only stub for the `UPDATE … SET <col>=<expr> WHERE <col> IS NULL` backfill; (4) `task migrations:gen NAME=lock_<col>_not_null` removes `Optional()` and locks the column to NOT NULL after the backfill has been deployed
- **AND** each of these steps SHALL be committed and deployed as a separate change

### Requirement: All migrations use a descriptive name

The system SHALL require a descriptive `NAME` argument for every migration-generation task and SHALL reject placeholder names. Names SHALL be lowercase, snake_case, and SHALL describe the change (e.g. `add_widget_count`, `backfill_orphan_chunks`).

#### Scenario: Acceptable names

- **WHEN** `task migrations:gen NAME=add_user_consent_columns` is run
- **THEN** the new migration files SHALL be named `<timestamp>_add_user_consent_columns.sql`

#### Scenario: Rejected names

- **WHEN** any of `task migrations:gen NAME=auto`, `task migrations:gen NAME=wip`, `task migrations:gen NAME=tmp`, `task migrations:gen NAME=""`, or `task migrations:gen NAME="quick fix"` is run
- **THEN** `cmd/generate-migrations` SHALL exit non-zero with a diagnostic explaining which name was rejected and why

## REMOVED Requirements

### Requirement: dbmate is the migration tool

**Reason**: dbmate is replaced by Goose (runtime applier) and Atlas (library-based diff generator). The custom `nix/dbmate-wrapper/` is removed; the `dbmate` and `dbmate-wrapper` binaries are no longer included in the dev shell or Docker images.

**Migration**: Operators using `dbmate` against ncps databases SHALL switch to `ncps migrate up`. On first invocation, ncps will detect a dbmate-format `schema_migrations` table and convert it to Goose's canonical schema in place. The translated migration files preserve the dbmate timestamp history, so installations on any prior ncps version (including v0.4 with partial SQLite-only history) adopt cleanly. Operators SHALL take a backup of their database before running `migrate up` on a dbmate-managed install for the first time, per the `CHANGELOG.md` note.

### Requirement: SQL queries are hand-written per dialect under `db/query.<engine>.sql`

**Reason**: Hand-written per-dialect SQL is replaced by Ent's fluent API. Compile-time-checked query construction supersedes the sqlc round-trip and eliminates the lockstep editing of three near-duplicate query files.

**Migration**: All call sites in `pkg/cache/`, `pkg/ncps/`, `pkg/server/`, and `cmd/` are rewritten to use the Ent client's fluent API. The `db/query.sqlite.sql`, `db/query.postgres.sql`, and `db/query.mysql.sql` files are deleted, along with the generated `pkg/database/{sqlitedb,postgresdb,mysqldb}/` packages and the hand-written `pkg/database/Querier` interface.
