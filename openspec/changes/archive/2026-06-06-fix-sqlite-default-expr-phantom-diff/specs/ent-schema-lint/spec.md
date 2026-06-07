## ADDED Requirements

### Requirement: `cmd/ent-lint` rejects the phantom-diff `DefaultExpr` form for CURRENT_TIMESTAMP

The system SHALL add a statically-enforced invariant (A6) to `cmd/ent-lint` that fails when any `ent/schema/*.go` field declares its DB default as `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}`. This form is forbidden because Ent emits it as a parenthesized Atlas `RawExpr` that Atlas's SQLite inspector does not round-trip, producing a perpetual phantom table rebuild in generated SQLite migrations (GitHub issue #1328). Authors SHALL instead use `entsql.Default("CURRENT_TIMESTAMP")`, which round-trips cleanly. The check SHALL emit a checklist line with the `A6` identifier, the file, and the offending field, and SHALL exit non-zero on violation.

#### Scenario: A6 — DefaultExpr CURRENT_TIMESTAMP is rejected

- **WHEN** a schema declares `field.Time("last_accessed_at").Annotations(entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"})`
- **THEN** `cmd/ent-lint` SHALL fail with a `[FAIL] A6` checklist message naming the file and field and recommending `entsql.Default("CURRENT_TIMESTAMP")`

#### Scenario: A6 — entsql.Default form passes

- **WHEN** every `CURRENT_TIMESTAMP` default in `ent/schema/*.go` is declared via `entsql.Default("CURRENT_TIMESTAMP")`
- **THEN** `cmd/ent-lint` SHALL emit `[PASS] A6` and SHALL NOT fail on the A6 invariant
