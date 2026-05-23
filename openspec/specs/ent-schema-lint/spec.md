# Ent Schema Lint Specification

## Purpose

`cmd/ent-lint` is a small Go binary that statically enforces the Ent codegen invariants which ncps's schemas can violate today. It parses `ent/schema/*.go` via the Go AST, runs a checklist of invariants, and exits non-zero on any failure. The binary is wired into `nix flake check` so CI fails whenever a schema regresses an invariant. Invariants whose triggering pattern is not present in the current schema tree (e.g. `*_ciphertext` fields, edge-bound `.Unique()` fields, `field.Enum(...)`) are documented as project conventions and enforced by code review until a follow-up change adds the corresponding static check.

## Requirements

### Requirement: `cmd/ent-lint` statically enforces the Ent codegen invariants that match patterns present in ncps today

The system SHALL provide a `cmd/ent-lint` Go binary that parses `ent/schema/*.go` via the Go AST and fails with a non-zero exit when any of the statically-enforced Ent codegen invariants is violated. As of this change, the statically-enforced invariants are: (A1) field-level `entsql.Check(...)` annotations are forbidden; (A2) `entsql.OnDelete(...)` annotations on `edge.From()` are forbidden; (A4) every `edge.To(...)` must have a reciprocal `edge.From(...).Ref(...)` on the target schema.

Two further invariants (A3 — `field.X(...).Unique()` on a column also bound by `edge.From().Field(...)`; A5 — every `field.Bytes("*_ciphertext")` declaration must chain `.Sensitive()`) and the snake_case enum-type convention (a `field.Enum(...)` needing `entsql.Annotation{Type: "<table>_<column>_enum"}`) are documented as project conventions in `CLAUDE.md` and enforced by code review. They are not yet wired into `cmd/ent-lint` because ncps's current `ent/schema/` tree contains no triggering pattern for any of them (no edge-bound unique fields, no `*_ciphertext` columns, no `field.Enum(...)` declarations). Implementations of these checks SHALL land in a follow-up change at the point a triggering pattern is introduced.

The expand-contract policy (forbidden DDL — `DROP COLUMN`, `DROP TABLE`, `RENAME`, `ADD ... NOT NULL` without `DEFAULT` on a populated column — in the newest migration file) and the schema↔SQL CHECK presence cross-validation are likewise documented under §`database-migrations` and `CLAUDE.md` and reviewed manually for v1; they may be migrated into `cmd/ent-lint` in a later change.

#### Scenario: A1 — field-level CHECK is rejected

- **WHEN** a schema declares `field.Int64("count").Annotations(entsql.Check("count >= 0"))`
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message naming the file, the field, and the A1 invariant

#### Scenario: A2 — OnDelete on edge.From is rejected

- **WHEN** a schema declares `edge.From(...).Ref(...).Annotations(entsql.Annotation{OnDelete: entsql.Cascade})`
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message referencing the A2 invariant

#### Scenario: A4 — one-sided edge.To is rejected

- **WHEN** a schema declares `edge.To("targets", Target.Type)` but the `Target` schema declares no `edge.From(...).Ref("targets")` for that edge
- **THEN** `cmd/ent-lint` SHALL fail with a checklist message referencing the A4 invariant and listing the orphan edge

#### Scenario: All statically-enforced invariants pass

- **WHEN** every schema satisfies A1, A2, and A4
- **THEN** `cmd/ent-lint` SHALL exit zero with a checklist-formatted summary indicating each invariant passed

### Requirement: `cmd/ent-lint` is wired into `nix flake check`

The system SHALL run `cmd/ent-lint` as part of `nix flake check` via a new `ent-lint-check` derivation. CI SHALL fail when the lint exits non-zero.

#### Scenario: CI run with a passing schema

- **WHEN** `nix flake check` is run against a tree where every statically-enforced invariant passes
- **THEN** the `ent-lint-check` derivation SHALL succeed and contribute to the overall CI green status

#### Scenario: CI run with a failing schema

- **WHEN** `nix flake check` is run against a tree where any statically-enforced invariant fails
- **THEN** the `ent-lint-check` derivation SHALL fail with the checklist output captured in the CI logs

### Requirement: `cmd/ent-lint` output is checklist-formatted and machine-grep-friendly

The system SHALL emit one line per check with a leading `[PASS]` or `[FAIL]` token, the invariant identifier (one of `A1`, `A2`, `A4` as of this change), and a free-text description. The binary SHALL exit non-zero if any line is `[FAIL]`.

#### Scenario: Output shape

- **WHEN** `cmd/ent-lint --root .` is invoked
- **THEN** stdout SHALL contain lines like `[PASS] A1 ent/schema/narinfo.go: no field-level entsql.Check` and (on failure) `[FAIL] A4 ent/schema/narinfo.go: edge.To("references") has no reciprocal edge.From().Ref()`
- **AND** the exit code SHALL be non-zero if and only if at least one `[FAIL]` line is emitted
