# Ent Schema Lint Specification

## ADDED Requirements

### Requirement: `cmd/ent-lint` enforces the five Ent codegen invariants statically

The system SHALL provide a `cmd/ent-lint` Go binary that parses `ent/schema/*.go` via the Go AST and fails with a non-zero exit when any of the five Ent codegen invariants is violated. The five invariants are: (A1) field-level `entsql.Check(...)` annotations are forbidden; (A2) `entsql.OnDelete(...)` annotations on `edge.From()` are forbidden; (A3) `field.X(...).Unique()` on a column also bound by `edge.From().Field(...)` is forbidden; (A4) every `edge.To(...)` must have a reciprocal `edge.From(...).Ref(...)` on the target schema; (A5) every `field.Bytes("*_ciphertext")` declaration must chain `.Sensitive()`.

#### Scenario: A1 — field-level CHECK is rejected

- **WHEN** a schema declares `field.Int64("count").Annotations(entsql.Check("count >= 0"))`
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message naming the file, the field, and the A1 invariant

#### Scenario: A2 — OnDelete on edge.From is rejected

- **WHEN** a schema declares `edge.From(...).Ref(...).Annotations(entsql.Annotation{OnDelete: entsql.Cascade})`
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message referencing the A2 invariant

#### Scenario: A3 — field-level Unique on edge-bound FK column is rejected

- **WHEN** a schema declares `field.String("user_id").Unique()` and another schema declares `edge.From(...).Field("user_id")` referencing the same column
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message referencing the A3 invariant on the field declaration

#### Scenario: A4 — one-sided edge.To is rejected

- **WHEN** a schema declares `edge.To("targets", Target.Type)` but the `Target` schema declares no `edge.From(...).Ref("targets")` for that edge
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message referencing the A4 invariant and listing the orphan edge

#### Scenario: A5 — ciphertext field without Sensitive is rejected

- **WHEN** a schema declares `field.Bytes("api_key_ciphertext")` without a chained `.Sensitive()` call
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message referencing the A5 invariant and the field

#### Scenario: All invariants pass

- **WHEN** every schema satisfies A1–A5
- **THEN** `cmd/ent-lint` SHALL exit zero with a checklist-formatted summary indicating each invariant passed

### Requirement: `cmd/ent-lint` enforces the snake_case enum-type convention

The system SHALL fail when an Ent `field.Enum(...)` declaration that generates a Postgres ENUM type lacks an `entsql.Annotation{Type: "<table>_<column>_enum"}` annotation that specifies a snake_case type name.

#### Scenario: Missing enum-type annotation

- **WHEN** a schema declares `field.Enum("status").Values("open", "closed")` without an `entsql.Annotation{Type: ...}` clause
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message stating that the enum type defaults to PascalCase and citing the snake_case convention

#### Scenario: PascalCase enum-type annotation rejected

- **WHEN** a schema declares `field.Enum("status").Annotations(entsql.Annotation{Type: "MyTableStatusEnum"})`
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message instructing the developer to use the `<table>_<column>_enum` snake_case form

### Requirement: `cmd/ent-lint` enforces the expand-contract policy on the newest migration files

The system SHALL parse the *newest* file (by timestamp prefix) in each `migrations/<dialect>/` directory and fail when it contains `DROP COLUMN`, `DROP TABLE`, `RENAME COLUMN`, `RENAME TABLE`, or an `ALTER COLUMN ... SET NOT NULL` / `MODIFY COLUMN ... NOT NULL` on a column that the *prior* migration history left nullable on a non-empty table.

#### Scenario: Newest migration contains DROP COLUMN

- **WHEN** the newest `migrations/postgres/*.sql` file contains `ALTER TABLE narinfos DROP COLUMN deriver;`
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message naming the file and the forbidden statement

#### Scenario: Newest migration contains ADD COLUMN ... NOT NULL on a populated table

- **WHEN** the newest migration adds a `NOT NULL` column without `DEFAULT` to an existing table (one that was created by a prior migration)
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message identifying the violation and pointing at the four-step NOT NULL recipe

#### Scenario: Newest migration is expand-only

- **WHEN** the newest migration only adds nullable columns, new tables, new indexes, new Postgres ENUM values, or `NOT NULL` columns to newly-created (empty) tables
- **THEN** `cmd/ent-lint` SHALL exit zero for the expand-contract check

### Requirement: `cmd/ent-lint` cross-checks CHECK annotations against generated SQL

The system SHALL, for each `entsql.Annotation.Checks` entry declared on an Ent schema, verify that the corresponding CHECK clause appears in *every* dialect's baseline migration files. It SHALL also fail on the inverse — CHECK clauses present in generated SQL with no matching schema annotation.

#### Scenario: A schema-declared CHECK is missing from one dialect

- **WHEN** `Blob.Annotations()` declares `Checks: {"blobs_file_size_nonneg": "file_size >= 0"}` and the SQLite baseline contains it but the Postgres baseline does not
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message naming the missing CHECK and the dialect

#### Scenario: Orphan CHECK in generated SQL

- **WHEN** the Postgres baseline contains `CHECK (some_col > 0)` but no schema annotation declares it
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message naming the orphan CHECK

### Requirement: `cmd/ent-lint` is wired into `nix flake check`

The system SHALL run `cmd/ent-lint` as part of `nix flake check` via a new `ent-lint-check` derivation. CI SHALL fail when the lint exits non-zero.

#### Scenario: CI run with a passing schema

- **WHEN** `nix flake check` is run against a tree where every invariant passes
- **THEN** the `ent-lint-check` derivation SHALL succeed and contribute to the overall CI green status

#### Scenario: CI run with a failing schema

- **WHEN** `nix flake check` is run against a tree where any invariant fails
- **THEN** the `ent-lint-check` derivation SHALL fail with the checklist output captured in the CI logs

### Requirement: `cmd/ent-lint` output is checklist-formatted and machine-grep-friendly

The system SHALL emit one line per check with a leading `[PASS]` or `[FAIL]` token, the invariant identifier (`A1`–`A5`, `enum-snake`, `expand-contract`, `check-presence`), and a free-text description. The binary SHALL exit non-zero if any line is `[FAIL]`.

#### Scenario: Output shape

- **WHEN** `cmd/ent-lint --root .` is invoked
- **THEN** stdout SHALL contain lines like `[PASS] A1 field-level CHECK annotations` and `[FAIL] expand-contract migrations/postgres/20260520000000_drop_col.sql contains DROP COLUMN`
- **AND** the exit code SHALL be non-zero if and only if at least one `[FAIL]` line is emitted
