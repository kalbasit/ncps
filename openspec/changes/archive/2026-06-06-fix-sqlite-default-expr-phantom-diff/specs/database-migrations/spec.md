## ADDED Requirements

### Requirement: Generated SQLite migrations are minimal and table-scoped

The system SHALL generate SQLite migrations whose DDL touches only the tables whose Ent schema actually changed. A schema change to one table SHALL NOT cause `cmd/generate-migrations` to emit a rebuild (create-new / copy / drop / rename swap) of an unrelated table. In particular, the Atlas-computed diff between the replayed SQLite migration state and the Ent desired schema SHALL be empty when no Ent schema field has changed.

To guarantee this, every DB-level `CURRENT_TIMESTAMP` column default SHALL be declared via `entsql.Default("CURRENT_TIMESTAMP")` (which Ent emits as a plain string default that Atlas's SQLite inspector round-trips exactly), and SHALL NOT be declared via `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` (which Ent emits as a parenthesized `RawExpr` that Atlas's SQLite inspector reads back without parentheses, producing a perpetual phantom `ModifyColumn ... ChangeDefault`).

#### Scenario: No-op generation when nothing changed

- **WHEN** `cmd/generate-migrations --name=<x>` is run for the SQLite dialect against the committed Ent schema with no field changes
- **THEN** no new `migrations/sqlite/*.sql` file SHALL be produced
- **AND** the Atlas diff SHALL report zero changes (no `ModifyTable` for `narinfos` or `nar_files`)

#### Scenario: Single-table change stays single-table

- **WHEN** a developer adds a field to one table (e.g. `nar_files`) and runs `task migrations:gen NAME=add_widget`
- **THEN** the generated `migrations/sqlite/<ts>_add_widget.sql` SHALL contain DDL only for `nar_files`
- **AND** SHALL NOT contain a `new_narinfos` table rebuild or any DDL for the unrelated `narinfos` table

#### Scenario: CURRENT_TIMESTAMP defaults round-trip cleanly

- **WHEN** an Ent `field.Time(...)` column declares a DB default of `CURRENT_TIMESTAMP`
- **THEN** it SHALL use `entsql.Default("CURRENT_TIMESTAMP")` so the generated `ent/migrate/schema.go` entry reads `Default: "CURRENT_TIMESTAMP"` (a string), not `Default: schema.Expr("CURRENT_TIMESTAMP")`
- **AND** the on-disk SQLite DDL SHALL remain `DEFAULT (CURRENT_TIMESTAMP)`, requiring no migration to existing databases
