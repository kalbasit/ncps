# Database ORM Specification

## ADDED Requirements

### Requirement: Ent schemas are the single source of truth for the database schema

The system SHALL define every table, column, index, foreign-key edge, and check constraint as Ent schema declarations under `ent/schema/*.go`. No hand-written SQL DDL SHALL exist outside of generated migration files and the test fixtures used to validate the linter.

#### Scenario: Adding a new table

- **WHEN** a developer needs a new table in the database
- **THEN** they SHALL create `ent/schema/<entity>.go` defining the entity's fields, edges, mixins, and annotations
- **AND** they SHALL NOT hand-edit any file under `migrations/`, `ent/<entity>/`, or any other generated location

#### Scenario: Modifying an existing column

- **WHEN** a developer needs to change a column's type, nullability, or default
- **THEN** they SHALL edit only the corresponding `ent/schema/*.go` field declaration
- **AND** the generated migration produced by `task migrations:gen` SHALL reflect that change for every supported dialect

### Requirement: The generated Ent client is committed to the repository

The system SHALL commit the entire generated `ent/` tree (the runtime client, predicates, mutations, hooks, and `migrate/schema.go`) to version control. CI SHALL fail if a `go generate ./ent/...` run produces a diff against the committed tree.

#### Scenario: Drift between schema and generated client

- **WHEN** a developer edits `ent/schema/*.go` but does not run `go generate ./ent/...`
- **THEN** the CI `ent-check` step SHALL fail with a non-zero exit and a diff of the generated files

#### Scenario: Clean clone build

- **WHEN** a developer clones the repository and runs `go build ./...`
- **THEN** the build SHALL succeed without first running `go generate`, because the generated tree is present in the working copy

### Requirement: The Ent client supports SQLite, PostgreSQL, and MySQL/MariaDB

The system SHALL produce a working Ent client for `dialect.SQLite`, `dialect.Postgres`, and `dialect.MySQL`. The dialect SHALL be selected at runtime from the `--cache-database-url` scheme.

#### Scenario: Opening against SQLite

- **WHEN** `--cache-database-url=sqlite:/var/cache/ncps/db.sqlite` is passed
- **THEN** `database.Open` SHALL return an Ent client configured with `dialect.SQLite` and the `mattn/go-sqlite3` driver

#### Scenario: Opening against PostgreSQL

- **WHEN** `--cache-database-url=postgresql://...` (or `postgres://`) is passed
- **THEN** `database.Open` SHALL return an Ent client configured with `dialect.Postgres` and the `jackc/pgx/v5/stdlib` driver

#### Scenario: Opening against MySQL/MariaDB

- **WHEN** `--cache-database-url=mysql://...` is passed
- **THEN** `database.Open` SHALL return an Ent client configured with `dialect.MySQL` and the `go-sql-driver/mysql` driver

### Requirement: Transactions are exposed through the Ent transaction API

The system SHALL expose database transactions via Ent's `*ent.Tx`. The cache layer's `withTransaction(name, fn)` wrapper SHALL be preserved, with its closure parameter reshaped to accept `*ent.Tx`.

#### Scenario: A multi-statement operation must be atomic

- **WHEN** code in `pkg/cache/` needs to insert a `nar_files` row and a `narinfo_nar_files` link in one atomic step
- **THEN** it SHALL use `c.withTransaction(ctx, "<name>", func(tx *ent.Tx) error { ... })` and call `tx.NarFile.Create()...Save(ctx)` followed by `tx.NarinfoNarFile.Create()...Save(ctx)` inside the closure
- **AND** if the closure returns an error, both inserts SHALL be rolled back

### Requirement: Schemas use mixins for shared timestamp columns

The system SHALL provide an Ent mixin (e.g. `entmixin.Timestamps`) that contributes `created_at` and `updated_at` fields with the project-standard types and defaults. Every schema with timestamp columns SHALL declare the mixin via `Mixin()` rather than re-declare the fields.

#### Scenario: A new schema declares standard timestamps

- **WHEN** a developer creates `ent/schema/<entity>.go` and the entity needs `created_at` / `updated_at`
- **THEN** the schema's `Mixin()` method SHALL return `[]ent.Mixin{entmixin.Timestamps{}}`
- **AND** the schema SHALL NOT re-declare `created_at` or `updated_at` fields directly

### Requirement: `Schema.Create` is the canonical fresh-install schema source

The system SHALL use Ent's runtime `entSchema.NewMigrate(drv).Create(ctx, migrate.Tables...)` as the schema-creation path for fresh installs (databases that have no application tables and no `schema_migrations` row). This produces the *current* end-state schema in one operation, rather than replaying historical migration files. The schema produced by `Schema.Create` MUST match the schema produced by applying every `.sql` file under `migrations/<dialect>/` in timestamp order; this equivalence is gated by the §8 schema-equivalence golden test.

#### Scenario: Empty DB fresh install

- **WHEN** `ncps migrate up` is invoked against an empty database
- **THEN** the system SHALL call `entSchema.NewMigrate(drv).Create(ctx, migrate.Tables...)` to produce the entire Ent-expected schema
- **AND** the system SHALL NOT execute any file under `migrations/<dialect>/` against that database

#### Scenario: Schema.Create == applied migrations

- **WHEN** the §8 golden test compares (a) a fresh database after Schema.Create against (b) a fresh database after `goose.Up` applies every `migrations/<dialect>/*.sql` in timestamp order
- **THEN** the two schemas SHALL be byte-equivalent (modulo column ordering) for every dialect

### Requirement: Hand-written engine-specific Querier code is removed

The system SHALL NOT contain the legacy `pkg/database/{sqlitedb,postgresdb,mysqldb}/` packages, the `pkg/database/generated_*.go` files, or the hand-written `pkg/database/Querier` interface after this change is applied. The Ent client is the sole database surface for callers in `pkg/cache/`, `pkg/ncps/`, `pkg/server/`, and `cmd/`.

#### Scenario: Verifying removal

- **WHEN** the change is applied
- **THEN** `pkg/database/sqlitedb/`, `pkg/database/postgresdb/`, `pkg/database/mysqldb/`, `pkg/database/generated_models.go`, `pkg/database/generated_errors.go`, `pkg/database/generated_querier.go`, and `pkg/database/generated_wrapper_{sqlite,postgres,mysql}.go` SHALL NOT exist in the repository
- **AND** no file under `pkg/` SHALL import any of those paths
