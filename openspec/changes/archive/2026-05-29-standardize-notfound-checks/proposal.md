## Why

The cache layer detects "row not found" inconsistently. `pkg/cache` calls `ent.IsNotFound(err)` directly at ~20 sites, while the purpose-built `database.IsNotFoundError(err)` helper is used exactly once. `database.IsNotFoundError` is the intended single predicate: it matches both Ent's `*NotFoundError` and the package-level `database.ErrNotFound` sentinel (which the cache test fakes return). Calling `ent.IsNotFound` directly means those call sites silently fail to recognize the sentinel, and the not-found policy is scattered instead of centralized.

## What Changes

- Replace direct `ent.IsNotFound(err)` checks in `pkg/cache` production code (`cache.go`, `build_trace.go`) with `database.IsNotFoundError(err)`, so the cache layer has one not-found predicate.
- This is behavior-preserving for real Ent errors (`database.IsNotFoundError` is a superset matcher) and strictly more correct for the sentinel: code paths that may receive a `database.ErrNotFound` now recognize it.
- Keep the `database.ErrNotFound` sentinel and the dual-mode check in `database.IsNotFoundError` — the cache test fakes return the sentinel, and after this change the production cache code correctly honors it, so the dual mode is now load-bearing rather than vestigial.
- Leave package-specific sentinels untouched: `storage.ErrNotFound`, `upstream.ErrNotFound`, `chunk.ErrNotFound`, and `config.ErrConfigNotFound` are distinct and matched via `errors.Is` as before. Out of scope: `pkg/ncps` and `pkg/config` (separate packages, separate change if desired).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `database-orm`: add a requirement that the cache layer detects not-found through the single `database.IsNotFoundError` predicate (which recognizes both Ent's `*NotFoundError` and the `database.ErrNotFound` sentinel) rather than calling `ent.IsNotFound` directly.

## Impact

- Code: `pkg/cache/cache.go` (~19 sites) and `pkg/cache/build_trace.go` (1 site, plus a new `database` import).
- No public API change, no schema/migration change. Behavior is identical for Ent errors and a superset for the sentinel.
- Tests: existing `pkg/cache` suite (including the fakes in `cache_internal_test.go` that return `database.ErrNotFound`) is the safety net.

## Non-goals

- Not removing the `database.ErrNotFound` sentinel or simplifying the dual-mode predicate.
- Not changing `pkg/ncps`, `pkg/config`, or `testhelper` call sites.
- Not altering any returned error values — only the predicate used to classify them.

## I/O, latency, memory impact

None. `database.IsNotFoundError` performs the same `errors.As`/type check as `ent.IsNotFound` plus one cheap `errors.Is` against a sentinel. No I/O, allocation, or latency change.
