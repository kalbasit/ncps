## Why

Post-Ent-migration, the mapping from ncps's `database.Type` to a dialect identifier is duplicated. `database.EntDialectFor` (`pkg/database/client.go`) is documented as "kept exported so `pkg/database/migrate` can share the same mapping" — yet `migrate` never calls it and instead carries a byte-for-byte copy (`entDialectFor` in `fresh.go`), plus its own duplicate `ErrUnknownDialect` sentinel. The duplication invites the copies to drift and contradicts the stated design.

## What Changes

- Remove the duplicate `entDialectFor` in `pkg/database/migrate/fresh.go` and route its single call site through the shared `database.EntDialectFor`, fulfilling that function's documented purpose.
- Collapse the duplicated `ErrUnknownDialect` sentinel to one source of truth (`database.ErrUnknownDialect`), so callers compare against a single error value.
- Evaluate the two goose dialect mappers — `gooseStoreDialectFor` (returns goose's `database.Dialect`) and `gooseDialectFor` (returns `goose.Dialect`). Merge them only if the goose types are convertible without added casts; otherwise leave both and document why they stay separate.
- Behavior-preserving: no schema, migration, or runtime behavior changes.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `database-orm`: add a requirement that the `database.Type` → ent dialect-string mapping has a single source of truth (`database.EntDialectFor`), reused by every package that needs it (including `pkg/database/migrate`), rather than re-implemented per package.

## Impact

- Code: `pkg/database/migrate/fresh.go`, `pkg/database/migrate/apply.go` (goose mapper review only), `pkg/database/client.go` (doc comment), and the `ErrUnknownDialect` declaration site(s).
- No public API removal: `database.EntDialectFor` stays exported (now actually consumed cross-package).
- Tests: existing `pkg/database/migrate/migrate_test.go` and `pkg/database/client_test.go` cover fresh-install and dialect mapping; they remain the safety net.

## Non-goals

- Not touching the goose runtime apply path semantics, migration files, or `atlas.sum`.
- Not changing dialect *coverage* (SQLite, PostgreSQL, MySQL all remain supported).
- Not forcing a merge of the two goose mappers if their return types are genuinely distinct.

## I/O, latency, memory impact

None. This is a compile-time deduplication of pure functions and a sentinel value; it adds no allocations and changes no I/O, network, or memory behavior.
