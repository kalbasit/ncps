## 1. Stress Reproduction (Red Phase)

- [x] 1.1 In `pkg/cache/cache_test.go`, add a sub-loop that runs `testConcurrentDownloadCancelOneClientOthersContinue` 50× in a tight inner loop under `-race` to surface the hang reliably
- [x] 1.2 Confirm the stress loop fails (hangs for 30 s then prints goroutine dump) on unmodified code, establishing a red baseline

## 2. Context-Aware `cond.Wait()` Loop

- [x] 2.1 In `pkg/cache/cache.go`, inside the per-client streaming SafeGo (regular path, ~line 1235), add a watcher goroutine before the loop that broadcasts on `ctx.Done()` and exits via a `defer close(watcherDone)` channel
- [x] 2.2 Add `ctx.Err() != nil` as an additional exit condition inside the `cond.Wait()` loop (alongside the existing `ds.downloadError != nil` check), and `return` when true
- [x] 2.3 Apply the same watcher + ctx-check to the decompression path (`cond.Wait()` inside `fileAvailableReader.Read` at ~line 494) if it is reachable from the test scenario
- [x] 2.4 Run the stress loop — confirm hang frequency decreases or disappears

## 3. Context-Aware Storage-Wait `select`

- [x] 3.1 In the regular streaming path (~line 1301), extend `select { case <-ds.stored: case <-ds.done: }` to also include `case <-ctx.Done():` with a debug-level log message
- [x] 3.2 In the decompression path (~line 1219), apply the same `ctx.Done()` extension
- [x] 3.3 Run the stress loop — confirm no remaining hangs

## 4. Verification

- [x] 4.1 Run `task test` with `-race` and confirm `TestCacheBackends/.../ConcurrentDownloadCancelOneClientOthersContinue` passes across all backends
- [x] 4.2 Run `task fmt` and `task lint` and fix any issues
- [x] 4.3 Remove or keep the 50× stress sub-loop depending on CI time budget (if kept, guard behind a build tag or short-test skip)
- [x] 4.4 Confirm the stress loop passes 50/50 iterations with the fix in place
