# Database Migrations Specification

## Overview

This spec defines the database migration system using `bun/migrate` with embedded SQL files, replacing the previous dbmate approach.

---

## Migration files are embedded in the binary

All database migration SQL files SHALL be embedded in the ncps binary at build time using `go:embed`. No external migration tool binary SHALL be required to run migrations.

#### Scenario: Binary contains migration files
- **WHEN** the ncps binary is built
- **THEN** migration SQL files for all three engines (sqlite, postgres, mysql) are embedded and accessible without any external file system paths

#### Scenario: No external dbmate binary needed
- **WHEN** a user runs `ncps migrate up` on a fresh system
- **THEN** migrations apply successfully without requiring dbmate or any other external binary to be installed

---

## Migration files follow bun/migrate naming convention

Each migration SHALL consist of two plain SQL files: `<version>_<name>.up.sql` for the forward migration and `<version>_<name>.down.sql` for the rollback. The version is a timestamp prefix in `YYYYMMDDHHmmss` format.

#### Scenario: Migration file naming
- **WHEN** a new migration is created
- **THEN** two files are produced: `<timestamp>_<name>.up.sql` and `<timestamp>_<name>.down.sql`

#### Scenario: Up and down SQL are separate files
- **WHEN** inspecting a migration directory
- **THEN** each logical migration is represented by exactly two files; no single-file `-- migrate:up` / `-- migrate:down` markers are used

---

## `ncps migrate` CLI command

ncps SHALL expose a `migrate` command with sub-commands to manage database schema migrations. The command SHALL accept a `--cache-database-url` flag identifying the target database.

#### Scenario: Apply all pending migrations
- **WHEN** the user runs `ncps migrate up`
- **THEN** all migrations not yet recorded in the migrations tracking table are applied in ascending version order

#### Scenario: Apply migrations up to a specific version
- **WHEN** the user runs `ncps migrate up-to <version>`
- **THEN** all migrations with version ≤ `<version>` that are not yet applied are applied; migrations with version > `<version>` are not touched

#### Scenario: Roll back the last migration
- **WHEN** the user runs `ncps migrate down`
- **THEN** the most recently applied migration is rolled back by executing its `.down.sql` file

#### Scenario: Roll back to a specific version
- **WHEN** the user runs `ncps migrate down-to <version>`
- **THEN** all migrations with version > `<version>` are rolled back in descending order

#### Scenario: Show migration status
- **WHEN** the user runs `ncps migrate status`
- **THEN** a table is printed listing every migration file, its version, name, and whether it has been applied or is pending

#### Scenario: Missing database URL
- **WHEN** the user runs `ncps migrate up` without providing `--cache-database-url`
- **THEN** the command exits with a non-zero status and a descriptive error message

---

## Migration state is tracked in the database

`bun/migrate` SHALL use its own tracking table (`bun_migrations`) to record applied migrations. For databases previously managed by dbmate, a compatibility migration SHALL rename the existing `schema_migrations` table so that already-applied migrations are not re-run.

#### Scenario: Fresh database
- **WHEN** `ncps migrate up` is run against a database with no existing tables
- **THEN** `bun_migrations` is created and all migrations are applied and recorded

#### Scenario: Existing dbmate-managed database
- **WHEN** `ncps migrate up` is run against a database that already has a `schema_migrations` table from dbmate
- **THEN** the compatibility migration recognises the existing applied migrations and does not re-apply them

---

## New migration files are plain SQL pairs

When a developer adds a new database migration, they SHALL create two plain SQL files (`.up.sql` and `.down.sql`) with the correct timestamp prefix. No code generation or external tool invocation is required beyond creating the files.

#### Scenario: Adding a migration
- **WHEN** a developer creates `db/migrations/sqlite/20260401000000_add_foo.up.sql` and `db/migrations/sqlite/20260401000000_add_foo.down.sql`
- **THEN** `ncps migrate up` picks up and applies the new migration on the next run

#### Scenario: Migration content is auditable
- **WHEN** reviewing a pull request that adds a migration
- **THEN** the SQL changes are visible as plain text diffs in `.up.sql` and `.down.sql` without generated code intermediaries
