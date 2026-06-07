## 1. Reconnaissance

- [x] 1.1 Confirm the HEAD fast-path in `getNar(false)` (pkg/server/server.go) and that `GetNarFileSize` (pkg/cache/cache.go) is a DB-only lookup; note exact lines.
- [x] 1.2 Confirm the servability helpers and signatures: `statNarInStore` (tri-state, unexported), `HasNarInChunks(bool,error)`, `HasNarInStore(bool)`; and how `GetNar` decides servability, so `IsNarServable` mirrors it.
- [x] 1.3 Find existing HEAD/NAR server tests and cache existence tests to model new tests on (e.g. `pkg/server/server_test.go` HEAD cases, `pkg/cache` stat/chunk tests).

## 2. TDD: Red — cache servability method

- [x] 2.1 Add a `pkg/cache` test: `IsNarServable` returns `(true,nil)` when a whole-file is in the store; `(true,nil)` when chunks exist; `(false,nil)` when a `nar_file` row exists but neither bytes nor chunks are present; `(false,err)` when the store stat is ambiguous (model on `ambiguousNarStore` in `ambiguous_storage_purge_internal_test.go`).
- [x] 2.2 Run and confirm RED (method does not exist yet / behavior absent).

## 3. TDD: Green — cache servability method

- [x] 3.1 Implement exported `Cache.IsNarServable(ctx, narURL) (bool, error)` mirroring `GetNar`'s determination: whole-file (`statNarInStore`) → true; else chunks (`HasNarInChunks`) → true; both confirmed absent → `(false,nil)`; ambiguous stat error → `(false,err)`.
- [x] 3.2 Run section 2 tests → GREEN.

## 4. TDD: Red — HEAD handler

- [x] 4.1 Add a `pkg/server` test: `HEAD /upload/nar/<h>.nar.zst` for a NAR with a `nar_file` row but no backing bytes returns `404` (not `200`). Seed the byteless record the way production does (e.g. via narinfo PUT / direct ent create), no NAR in the store.
- [x] 4.2 Add a `pkg/server` test: `HEAD` for a NAR whose bytes ARE present returns `200` with `Content-Length` (fast path preserved).
- [x] 4.3 Run and confirm RED on 4.1 (today it returns `200` from the DB size).

## 5. TDD: Green — HEAD handler

- [x] 5.1 In `getNar(false)`, emit `200` from the `GetNarFileSize` size only when `IsNarServable` returns `(true,nil)`; otherwise fall through to `s.cache.GetNar(...)` (which handles upload-only `404` and substituter recovery, and writes no body for HEAD).
- [x] 5.2 Run sections 4 and 2 tests → GREEN; confirm the servable-NAR fast path still returns `200`.

## 6. Verification

- [x] 6.1 `task fmt` exits 0.
- [x] 6.2 `task lint` exits 0 (any `//nolint` carries a comment).
- [x] 6.3 `task test` exits 0 (race on; all subtests call `t.Parallel()`).
- [x] 6.4 Diff review against proposal/design scope: HEAD existence check + one new cache method only; no `GetNar`/narinfo/write-path/schema/route changes.
