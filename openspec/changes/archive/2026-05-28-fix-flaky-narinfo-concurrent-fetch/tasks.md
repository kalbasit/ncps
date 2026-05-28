## 1. Write failing test

- [x] 1.1 Add `TestGetNarInfo_ConcurrentFetch` to `pkg/cache/cache_test.go`: launch N goroutines that call `GetNarInfo` for the same hash simultaneously against a cold cache with a slow upstream stub (add ~10ms delay), assert all return the narinfo with no error
- [x] 1.2 Run `task test` and confirm the new test fails (reproduces the race) before any production changes

## 2. Fix purge guard in getNarInfoFromDatabase

- [x] 2.1 Extend `hasUpstreamJob` (or add `hasAnyUpstreamJobForHash`) in `pkg/cache/cache.go` to return true when EITHER `narInfoJobKey(hash)` OR `narJobKey(hash)` is registered in `upstreamJobs`
- [x] 2.2 Update the purge guard block in `getNarInfoFromDatabase` to call the extended helper so that a NAR download in-flight also prevents the spurious purge
- [x] 2.3 Run `task test` and confirm the new `TestGetNarInfo_ConcurrentFetch` now passes

## 3. Verify existing tests still pass

- [x] 3.1 Run `task test` for the full `pkg/cache` and `pkg/server` packages with `-race` and confirm `TestGetNar_NixServeUpstream` and all other narinfo tests pass cleanly
- [x] 3.2 Run `task fmt` and `task lint` — fix any formatting or lint issues before proceeding

## 4. Completion checks

- [x] 4.1 Run `task fmt` — confirm exits 0 with no changes
- [x] 4.2 Run `task lint` — confirm exits 0
- [x] 4.3 Run `task test` — confirm all tests pass with race detector
