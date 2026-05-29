## Context

The Ent migration left two parallel implementations of the same `database.Type` → dialect mapping:

- `database.EntDialectFor(t Type) (string, error)` in `pkg/database/client.go`, documented as "kept exported so `pkg/database/migrate` can share the same mapping."
- `entDialectFor(d ncpsdb.Type) (string, error)` in `pkg/database/migrate/fresh.go` — a byte-for-byte copy that the migrate package actually uses.

Both packages also declare their own `ErrUnknownDialect` sentinel; `client.go`'s declaration even comments "Mirrors the sentinel in pkg/database/migrate." The migrate package imports the database package as `ncpsdb` (the identifier `database` inside migrate refers to goose's `github.com/pressly/goose/v3/database`), so the shared function is reachable as `ncpsdb.EntDialectFor`.

Separately, two goose mappers exist: `gooseStoreDialectFor` (`fresh.go`, returns goose's `database.Dialect`) and `gooseDialectFor` (`apply.go`, returns `goose.Dialect`). These return *different* goose types.

## Goals / Non-Goals

**Goals:**
- One source of truth for the `Type` → ent dialect string mapping, reused cross-package.
- One `ErrUnknownDialect` sentinel that `errors.Is` matches consistently.
- Behavior-preserving: identical dialect resolution and identical error semantics.

**Non-Goals:**
- Changing goose apply behavior, migration files, or dialect coverage.
- Forcing a merge of the two goose mappers if their return types are genuinely distinct.

## Decisions

**Decision 1 — Delete `migrate.entDialectFor`; call `ncpsdb.EntDialectFor`.**
The exported function's documented contract already names this exact reuse. Alternative (unexport `EntDialectFor`) was rejected: it contradicts the doc comment and the cross-package need is real.

**Decision 2 — Single `ErrUnknownDialect`.** Make the migrate package reference `ncpsdb.ErrUnknownDialect` rather than declare its own, and drop the "Mirrors the sentinel" comment. This keeps `errors.Is(err, database.ErrUnknownDialect)` true regardless of which layer produced the error. Error *message* wrapping (the `"entDialectFor: ..."` prefix) is internal and not asserted by tests, so consolidating the sentinel is safe.

**Decision 3 — Goose mappers: inspect, then decide.** Confirm the concrete types of `goose.Dialect` vs goose's `database.Dialect`. If one is an alias/convertible to the other without a cast, collapse to a single helper returning the broader type. If they are distinct exported types (the likely case), leave both functions and add a one-line comment explaining they target different goose APIs. No casts will be introduced solely to merge them.

## Risks / Trade-offs

- [Consolidating `ErrUnknownDialect` could change which sentinel a test matches] → Audit `errors.Is(..., ErrUnknownDialect)` / `ErrorIs` call sites first; the migrate tests assert behavior (fresh install succeeds, unknown dialect errors), not a specific sentinel identity. Run the full migrate suite to confirm.
- [Import cycle risk] → None: `migrate` already imports the database package as `ncpsdb`; the dependency direction is unchanged.

## Migration Plan

Pure code refactor, no DB migration. Deploy as a single stacked PR; rollback is a straight revert. No runtime state involved.

## Open Questions

- Whether `goose.Dialect` and goose's `database.Dialect` are convertible — resolved during implementation by reading the goose v3 types; the spec requirement does not depend on the outcome.
