# Database ORM Specification

## Purpose

ncps uses [Ent](https://entgo.io/) as its type-safe ORM. Tables, columns, indexes, edges, and CHECK constraints are declared as Ent schemas under `ent/schema/*.go`; the generated Ent client (committed under `ent/`) is the sole database surface for the cache layer and CLI commands. This capability captures the schema-as-source-of-truth contract, the dialect coverage, the transaction surface, the shared timestamp mixin, the `Schema.Create` fresh-install path, and the removal of the legacy sqlc-based engine packages.

## Requirements

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

### Requirement: The dialect-string mapping has a single source of truth

The system SHALL define the mapping from `database.Type` to its Ent dialect string in exactly one place — the exported `database.EntDialectFor` function — and every package that needs that mapping (including `pkg/database/migrate`) SHALL call `database.EntDialectFor` rather than re-implement it. The system SHALL likewise expose a single `database.ErrUnknownDialect` sentinel that all callers return and match via `errors.Is`, rather than per-package duplicate sentinels.

#### Scenario: Migrate package resolves the ent dialect

- **WHEN** `pkg/database/migrate` needs the Ent dialect string for a `database.Type`
- **THEN** it SHALL obtain it by calling `database.EntDialectFor(t)`
- **AND** no second copy of the `Type` → ent dialect-string switch SHALL exist under `pkg/database/migrate/`

#### Scenario: Unknown dialect produces the shared sentinel

- **WHEN** `database.EntDialectFor` is called with `database.TypeUnknown` (or any unmapped `Type`)
- **THEN** it SHALL return an error that satisfies `errors.Is(err, database.ErrUnknownDialect)`
- **AND** callers across `pkg/database` and `pkg/database/migrate` SHALL match that same sentinel value

#### Scenario: Dialect coverage is unchanged

- **WHEN** `database.EntDialectFor` is called with `TypeSQLite`, `TypePostgreSQL`, or `TypeMySQL`
- **THEN** it SHALL return `dialect.SQLite`, `dialect.Postgres`, and `dialect.MySQL` respectively, exactly as before this change

### Requirement: Common by-hash and aggregate reads go through shared cache query helpers

The cache layer SHALL obtain the common single-entity-by-hash lookups and the total-NAR-file-size aggregate through shared `pkg/cache` query helpers rather than re-inlining the equivalent Ent query at each call site. Each helper SHALL accept the Ent entity client (e.g. `*ent.NarInfoClient`) so the same helper serves both `*ent.Client` and `*ent.Tx` callers, and SHALL preserve the underlying Ent semantics exactly.

#### Scenario: NarInfo-by-hash helper returns the row or an Ent not-found error

- **WHEN** the NarInfo-by-hash helper is called with a hash that exists
- **THEN** it SHALL return the matching `*ent.NarInfo`
- **AND WHEN** called with a hash that does not exist
- **THEN** it SHALL return an error for which `ent.IsNotFound` reports true — identical to the inline `Query().Where(HashEQ).Only(ctx)` it replaces

#### Scenario: Total-NAR-file-size helper sums file_size and is zero-safe

- **WHEN** the total-size helper is called and `nar_files` contains rows
- **THEN** it SHALL return the sum of `file_size` across all rows as an `int64`
- **AND WHEN** there are no rows (or the sum is SQL NULL)
- **THEN** it SHALL return `0` without error

#### Scenario: Same helper serves transactional and non-transactional callers

- **WHEN** a helper is invoked with `tx.<Entity>` inside a transaction and elsewhere with `c.dbClient.Ent().<Entity>` outside one
- **THEN** both invocations SHALL execute the same query against their respective connection/transaction
- **AND** no call site SHALL re-declare the extracted query inline

### Requirement: The cache layer detects not-found through a single predicate

The cache layer (`pkg/cache`) SHALL classify "database row not found" exclusively through the `database.IsNotFoundError` predicate, rather than calling `ent.IsNotFound` directly. `database.IsNotFoundError` SHALL report true for both Ent's `*NotFoundError` and the package-level `database.ErrNotFound` sentinel, so a single helper governs the cache layer's not-found policy. Package-specific sentinels (`storage.ErrNotFound`, `upstream.ErrNotFound`, `chunk.ErrNotFound`, `config.ErrConfigNotFound`) remain distinct and continue to be matched via `errors.Is`.

#### Scenario: Ent missing-row error is classified as not-found

- **WHEN** a `pkg/cache` query returns Ent's `*NotFoundError`
- **THEN** the cache layer SHALL classify it as not-found via `database.IsNotFoundError`
- **AND** the resulting behavior SHALL be identical to the previous direct `ent.IsNotFound` check

#### Scenario: The not-found sentinel is recognized

- **WHEN** a code path receives `database.ErrNotFound` (e.g. from a test fake or a helper that returns the sentinel)
- **THEN** `database.IsNotFoundError` SHALL report true for it
- **AND** the cache layer SHALL treat it as a missing row rather than an unexpected error

#### Scenario: Unrelated package sentinels are not conflated

- **WHEN** an error is `storage.ErrNotFound`, `upstream.ErrNotFound`, `chunk.ErrNotFound`, or `config.ErrConfigNotFound`
- **THEN** it SHALL continue to be matched by its own `errors.Is` check
- **AND** `database.IsNotFoundError` SHALL NOT be used in place of those package-specific matches
