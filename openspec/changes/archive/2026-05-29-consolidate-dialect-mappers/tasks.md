## 1. Audit & baseline

- [x] 1.1 Confirm current behavior is green: run `task test` for `pkg/database/...` so the dialect-mapping/fresh-install tests pass before any change.
- [x] 1.2 Grep all references to `entDialectFor`, `EntDialectFor`, and `ErrUnknownDialect` across the repo to enumerate every call/match site that the consolidation must keep working.
- [x] 1.3 Inspect the goose types: determine whether `goose.Dialect` and goose's `github.com/pressly/goose/v3/database` `Dialect` are the same/convertible type, to decide if the two goose mappers can merge. (Confirmed: `goose.Dialect = database.Dialect` is a type alias — they are the same type.)

## 2. Consolidate the ent dialect mapping

- [x] 2.1 Delete `entDialectFor` from `pkg/database/migrate/fresh.go` and replace its call site with `ncpsdb.EntDialectFor(d)`, preserving error handling. (Also dropped the now-unused `entgo.io/ent/dialect` import.)
- [x] 2.2 Make `pkg/database/migrate` use the single `database.ErrUnknownDialect`; removed the migrate-local duplicate `ErrUnknownDialect` declaration and updated all call sites in `state.go`, `adopt.go`, `apply.go`.
- [x] 2.3 In `pkg/database/client.go`, update the `EntDialectFor` doc comment to reflect that migrate now consumes it, and drop the "Mirrors the sentinel" comment on `ErrUnknownDialect`.

## 3. Goose mappers

- [x] 3.1 Goose dialect types are the same (alias), so merged: removed `gooseStoreDialectFor` and routed `fresh.go` through the shared `gooseDialectFor` (its `goose.Dialect` return is assignable to goose-store's `database.Dialect`).

## 4. Verify

- [x] 4.1 Ran `task fmt` (3 files reformatted) and `task lint` (0 issues).
- [x] 4.2 Ran `task test` — full suite green, including `pkg/database` and `pkg/database/migrate`.
- [x] 4.3 No new test added: existing `migrate_test.go` / `client_test.go` already cover dialect resolution and fresh install; the refactor is behavior-preserving with no new edge case.
