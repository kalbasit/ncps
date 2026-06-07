## Why

`task migrations:gen` emits, **for SQLite only**, a full destructive table-rebuild of `narinfos` and `nar_files` on *every* new migration — even when the schema change touches a different table (GitHub issue #1328). Each new SQLite migration must be hand-trimmed; a missed trim ships a spurious, destructive table swap. The root cause was traced precisely: it is **not** the `narinfos` CHECK constraints (as the issue hypothesized) and **cannot** be resolved by a corrective migration.

The `last_accessed_at` columns on `narinfos` and `nar_files` declare their DB default via `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}`. Ent codegen emits this as `schema.Expr("CURRENT_TIMESTAMP")` (an Atlas `RawExpr`). Atlas's SQLite planner renders a `RawExpr` default parenthesized — desired default `(CURRENT_TIMESTAMP)` — but Atlas's SQLite **inspector** strips the parens when reading the stored DDL back, yielding current default `CURRENT_TIMESTAMP`. The two never compare equal, so Atlas emits a perpetual `ModifyColumn last_accessed_at` (ChangeDefault) for the only two tables carrying that column, which on SQLite forces a whole-table rebuild. `created_at` (via the `Timestamps` mixin's `entsql.Default("CURRENT_TIMESTAMP")`) is immune because the plain-string form round-trips cleanly.

## What Changes

- Change `last_accessed_at` in `ent/schema/narinfo.go` and `ent/schema/nar_file.go` from `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` to `entsql.Default("CURRENT_TIMESTAMP")` (matching the `Timestamps` mixin's `created_at`), then regenerate the Ent client.
- The emitted on-disk DDL is **identical** (`DEFAULT (CURRENT_TIMESTAMP)`), so **no migration file is produced or required** for any dialect. Existing databases are untouched.
- Add a `cmd/ent-lint` static guard forbidding `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` (the phantom-diff-producing form) and steering authors to `entsql.Default(...)`, preventing regression.
- Verified empirically: after the change, `task migrations:gen` (SQLite) emits no spurious migration; the diff is clean.

## Non-goals

- No change to Postgres or MySQL migration output (already clean).
- No corrective/data migration and no edit to any existing migration file (forbidden by the forward-only policy, and unnecessary here).
- No change to runtime default-value behavior; `last_accessed_at` still defaults to `CURRENT_TIMESTAMP` at the DB level.
- Not a general audit of every `DefaultExpr` usage beyond the `CURRENT_TIMESTAMP` phantom-diff case.

## Capabilities

### New Capabilities

_(none)_

### Modified Capabilities

- `database-migrations`: add a requirement that SQLite migration generation produces minimal, table-scoped diffs (no spurious rebuild of unrelated tables), and that DB `CURRENT_TIMESTAMP` defaults are declared via the round-trippable `entsql.Default(...)` form.
- `ent-schema-lint`: add a statically-enforced invariant rejecting `entsql.Annotation{DefaultExpr: "CURRENT_TIMESTAMP"}` in favor of `entsql.Default("CURRENT_TIMESTAMP")`.

## Impact

- Code: `ent/schema/narinfo.go`, `ent/schema/nar_file.go`, regenerated `ent/` client, `cmd/ent-lint`.
- No runtime I/O, network-latency, or memory impact — the change is schema-representation only and alters no SQL DDL, query path, or stored data.
- Developer-experience impact: future SQLite migrations are trustworthy and need no hand-trimming for these tables.
