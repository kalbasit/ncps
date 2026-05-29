## 1. Reproduce the 500 with a failing test (RED)

- [x] 1.1 Add a test that holds the download lock for a hash (simulating a slow/failed holder on another replica) while a cache instance requests the same NAR, and assert the request currently returns the coordination error mapped to HTTP 500. Confirm it FAILS against current behavior in the intended end state (i.e. it should expect success/404, not 500). — `pkg/cache/coordination_internal_test.go:TestCoordinateDownloadTakesOverNAR`, deterministic via a `takeoverLocker` mock (no Redis required).
- [x] 1.2 Add a sibling test for the failed-holder case: holder acquires the lock then releases it WITHOUT the asset appearing (download failed); assert the waiter should take over and succeed (failed before the fix). — covered by the same `TestCoordinateDownloadTakesOverNAR` (block → release → assert serve).
- [x] 1.3 Add a genuine-absence case: hash does not exist upstream; assert the lock-losing waiter resolves to a cache miss / HTTP 404 (not 500). All call `t.Parallel()` and run under the race detector. — `TestCoordinateDownloadNarInfoMissReturnsNotFound` (narinfo route) and `TestCoordinateDownloadNARGiveUpReturnsNotFound` (NAR route: sustained contention → give-up → `storage.ErrNotFound`, also covering the slow/stuck-holder scenario).

## 2. Distinguish holder terminal state in coordinateDownload (GREEN)

- [x] 2.1 In `coordinateDownload` (`pkg/cache/cache.go`), replaced the poll-only fallback loop with a poll-or-reacquire loop: each tick checks `hasAsset(coordCtx)` AND re-attempts `c.downloadLocker.Lock(...)`.
- [x] 2.2 On successful re-acquisition, `break pollLoop` falls through into the existing post-lock holder path (refresher, double-check, `startJob`, release strategy) so this replica takes over the download.
- [x] 2.3 Bound the overall wait by `max(c.downloadLockTTL, c.downloadPollTimeout)` (TTL as the primary bound, poll-timeout as a lower bound for config compatibility) instead of the fixed `downloadPollTimeout`, so a legitimately slow holder is not abandoned prematurely.
- [x] 2.4 The loop never starts a concurrent download while the holder still holds the lock: it only proceeds to `startJob` after re-acquire succeeds, and the `upstreamJobs` map still de-dupes within the process.

## 3. Map terminal give-up to a cache miss, not 500

- [x] 3.1 On terminal give-up (deadline reached, asset absent, re-acquire never succeeded), the coordination path now wraps `storage.ErrNotFound` instead of the generic "failed to acquire download lock..." error; caller cancellation still surfaces as the context error.
- [x] 3.2 Verified the server handlers (`pkg/server/server.go`) map `storage.ErrNotFound` → HTTP 404 for both NAR (`:835`) and narinfo (`:359`) routes, and context errors → no response. No server change needed.
- [x] 3.3 Added structured logs (`Warn` on give-up, `Debug` on take-over) AND a Prometheus counter `ncps_download_coordination_fallback_total{outcome=served_by_peer|take_over|give_up|caller_canceled}` so operators can observe how lock contention resolves.

## 4. Verify and harden

- [x] 4.1 Ran the new tests with the race detector for the default (SQLite) backend; both RED tests from section 1 now pass (GREEN). Full `pkg/cache` suite passes.
- [x] 4.2 Ran `task test:auto` (fresh services incl. Redis on random ports, full suite, teardown): the Redis-backed `pkg/cache` distributed tests and `pkg/lock/redis` pass, exercising the real cross-pod lock path.
- [x] 4.3 No concurrent same-hash download/chunking is triggered: take-over only runs after re-acquire, and `upstreamJobs` de-dupes; the CDC concurrent-reconstruction path (#1289) is not newly exercised.
- [x] 4.4 Cache-warm and single-request paths are unchanged: the fallback loop is only entered when the initial lock acquisition fails (contention); the existing `pkg/cache` suite (including warm-cache and single-request tests) passes.

## 5. Finalize

- [x] 5.1 Ran `task fmt` (0 changed), `task lint` (0 issues), and `task test` (all packages pass); each exits zero.
- [x] 5.2 Updated `design.md` with a "Resolved During Implementation" section: deadline = `max(downloadLockTTL, downloadPollTimeout)`, logs-only observability, helper-extraction shape, and test coverage.
