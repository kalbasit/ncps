## ADDED Requirements

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
