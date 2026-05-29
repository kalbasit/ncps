## Context

`database.IsNotFoundError(err)` (in `pkg/database/errors.go`) is defined as:

```go
return errors.Is(err, ErrNotFound) || isEntNotFound(err) // ent.IsNotFound
```

It is the intended single not-found predicate but is used once (`cache.go:5259`). Meanwhile `pkg/cache` calls `ent.IsNotFound(err)` directly at ~20 sites. The cache test fakes in `cache_internal_test.go` return `database.ErrNotFound`; production code that uses `ent.IsNotFound` would not classify that sentinel as not-found.

`cache.go` already imports `pkg/database`; `build_trace.go` does not (its single site will need the import added).

## Goals / Non-Goals

**Goals:**
- One not-found predicate (`database.IsNotFoundError`) across the cache package.
- Behavior-preserving for Ent errors; strictly-more-correct for the sentinel.

**Non-Goals:**
- Removing the sentinel or the dual-mode check.
- Touching `pkg/ncps`, `pkg/config`, `testhelper`, or non-database sentinels (`storage`/`upstream`/`chunk`/`config` ErrNotFound).

## Decisions

**Decision 1 — Mechanical replacement, preserving negation.**
Each `ent.IsNotFound(x)` becomes `database.IsNotFoundError(x)`; `!ent.IsNotFound(x)` becomes `!database.IsNotFoundError(x)`. The argument variable (err, nrErr, batchErr, …) is preserved exactly. No surrounding control flow changes.

**Decision 2 — Keep `database.ErrNotFound` + dual mode.**
The test fakes return the sentinel; standardizing on `database.IsNotFoundError` makes the production cache paths honor it, so the dual-mode branch becomes load-bearing. Removing it would break the fakes and reduce robustness. Alternative (migrate fakes to return a real `*ent.NotFoundError`) rejected as out-of-scope churn with no benefit.

**Decision 3 — Leave the `narInfoByHash` doc comment as-is.**
`queries.go` documents that the helper "returns an error for which `ent.IsNotFound` reports true" — still accurate (the helper returns Ent's error). Callers classify it via `database.IsNotFoundError`, which is a superset. No comment change needed beyond accuracy.

## Risks / Trade-offs

- [A site that intentionally wanted Ent-only matching gets broadened to also match the sentinel] → No such site exists: the sentinel is a not-found signal everywhere, so broadening is always correct. Confirmed by reviewing each of the ~20 sites — all treat the predicate as "is this a missing row?".
- [Adding the `database` import to `build_trace.go` creates an import cycle] → None: `pkg/cache` already depends on `pkg/database` (via `cache.go`); adding the import to a second file in the same package changes nothing structurally.

## Migration Plan

Pure code refactor in one package, single stacked PR, straight revert to roll back. No DB/runtime state.

## Open Questions

- None. Whether to extend to `pkg/ncps`/`pkg/config` is deferred; this change is scoped to the cache package.
