## 1. Reconnaissance

- [x] 1.1 Confirm the purge-guard block in `getNarInfoFromDatabase` (pkg/cache/cache.go, the `purgeNarInfo` call ~cache.go:4357) and the upload-only short-circuit in `GetNarInfo` (~cache.go:3432) still match the design; note exact line numbers.
- [x] 1.2 Locate existing purge-guard tests (`pkg/cache/cache_internal_test.go`, `pkg/server/purge_serving_test.go`, `narinfo-purge-serving` coverage) to model the new tests on, and confirm `WithUploadOnly`/`IsUploadOnly` helpers are reachable from the test package.

## 2. TDD: Red — failing tests

- [x] 2.1 Add a cache-level test: with a narinfo in the DB but its NAR absent and no download in flight, calling `GetNarInfo` with an upload-only context returns a not-found result (HTTP-404-equivalent) **and** leaves the narinfo + `nar_file` rows intact (assert `purgeNarInfo` did NOT run — rows still present after the call).
- [x] 2.2 Add a test asserting repeated upload-only reads of the same missing-NAR narinfo are deterministic (every call → not-found) and non-mutating (DB rows identical before/after), and that no upstream fetch is attempted.
- [x] 2.3 Add/confirm a regression test that the **non-upload-only** path is unchanged: the purge guard still fires (rows deleted) and still falls through to upstream re-fetch.
- [x] 2.4 Run the new tests and confirm they FAIL against current code (red), for the expected reason (rows are deleted under upload-only today).

## 3. TDD: Green — implementation

- [x] 3.1 In the purge-guard block of `getNarInfoFromDatabase`, add an `if IsUploadOnly(ctx) { return nil, ErrNarInfoPurged }` branch immediately before the "requesting a purge" log + `purgeNarInfo` call, with an explanatory comment (per `nolint`/comment conventions and the design rationale).
- [x] 3.2 Verify the returned sentinel routes through `GetNarInfo`'s upload-only short-circuit (~cache.go:3432) to `storage.ErrNotFound` → HTTP 404, with no upstream fetch and no destructive delete.
- [x] 3.3 Run the tests from section 2 and confirm they now PASS (green), and that the non-upload-only regression test still passes.

## 4. Verification

- [x] 4.1 `task fmt` exits 0.
- [x] 4.2 `task lint` exits 0 (any `//nolint` carries an explanatory comment).
- [x] 4.3 `task test` exits 0 (race detector on; all subtests call `t.Parallel()`).
- [x] 4.4 Re-read the diff against `proposal.md`/`design.md` scope: read-path only, no write-path/`PutNarInfo` change, no schema/migration, root path untouched.
