## Context

`Client.DB()` returns the raw `*sql.DB`; its doc comment discourages new use in favor of the Ent API. Tests use it ~110 times. A categorization shows most uses are appropriate:

- **Migration verification** (`pkg/ncps/migrate_*_test.go`): asserts row counts after a data migration — must be ORM-independent.
- **Adversarial setup** (`ExecContext` INSERT/UPDATE/DELETE to invalid states): Ent would reject or hide these.
- **Persistence inspection** (multi-column `created_at`/`last_accessed_at` reads): verifies DB-level timestamp behavior.
- **Pool/admin** (`SetMaxOpenConns`, `Close`, `CREATE/DROP DATABASE`, schema probes): genuinely need `*sql.DB`.
- **`DB()` self-test** (`client_test.go`).

The one clean-swap category is bare table row-count assertions in cache/server behavior tests:
- `pkg/cache/cache_test.go`: `SELECT COUNT(*) FROM <table>` (no predicate) → `dbClient.Ent().<Entity>.Query().Count(ctx)`.
- `pkg/server/server_test.go`: `SELECT COUNT(*) FROM narinfos WHERE hash = ?` → `dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(h)).Count(ctx)`.

## Goals / Non-Goals

**Goals:**
- Replace bare/by-hash `COUNT(*)` assertions in the two behavior-test files with Ent `.Count(ctx)`.
- Keep identical assertion outcomes.

**Non-Goals:**
- Removing `DB()` or converting the retained categories above.

## Decisions

**Decision 1 — Convert only COUNT(*) row-count assertions.** This is the only read pattern where Ent is strictly cleaner and raw SQL adds no verification independence (a whole-table or by-key count has no ordering/NULL/timestamp nuance). Alternative (also converting single/multi-column reads) rejected: timestamp/precision comparisons are fiddly and raw SQL there is legitimate persistence-layer verification.

**Decision 2 — Keep migration tests on raw SQL.** `pkg/ncps/migrate_*` verify that a data migration produced the right rows; using Ent (the same client the migration uses) would weaken the check. Their `COUNT(*)` stays raw by design — this is the documented boundary.

**Decision 3 — `server_test.go` gains an `entnarinfo` import** for the `HashEQ` predicate. `cache_test.go` needs no new predicate import (bare counts).

## Risks / Trade-offs

- [Ent `Count` return type / value mismatch vs the scanned `int`] → Ent's `Count(ctx)` returns `(int, error)`; assertions compare `int`. Identical. Verified by running the suites.
- [A "count" site actually carries a non-hash predicate] → Inspect each of the 10 sites; only convert bare-table or `WHERE hash = ?` forms. Anything else stays raw.

## Migration Plan

Test-only refactor, single stacked PR, straight revert. No production or DB state.

## Open Questions

- None. The boundary (convert behavior-test counts; keep migration/adversarial/inspection/admin raw SQL) is explicit and documented in the spec.
