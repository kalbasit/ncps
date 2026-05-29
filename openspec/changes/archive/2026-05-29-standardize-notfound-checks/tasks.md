## 1. Baseline & inventory

- [x] 1.1 Confirm `pkg/cache` suite is green before changes (committed Change 2 state).
- [x] 1.2 Listed every `ent.IsNotFound(` call site in `pkg/cache/cache.go` (19) and `pkg/cache/build_trace.go` (1), including negated forms.

## 2. Standardize the predicate

- [x] 2.1 Replaced every `ent.IsNotFound(x)` with `database.IsNotFoundError(x)` in `pkg/cache/cache.go` (negations preserved, arguments unchanged).
- [x] 2.2 Replaced the single site in `pkg/cache/build_trace.go` and added the `github.com/kalbasit/ncps/pkg/database` import.
- [x] 2.3 Verified `ent` is still used in both files (`ent.Tx`, `ent.BuildTraceSignatureCreate`, entity clients), so the import stays. The only remaining `ent.IsNotFound` reference in `pkg/cache` is the accurate doc comment in `queries.go`.

## 3. Verify

- [x] 3.1 Ran `task fmt` and `task lint` — 0 issues.
- [x] 3.2 Ran `task test` — full suite green (0 failures), including `pkg/cache` (32s) with the fakes that return `database.ErrNotFound`.
- [x] 3.3 No new test added: existing cache tests already cover the not-found paths; behavior is identical for Ent errors and a correct superset for the sentinel.
