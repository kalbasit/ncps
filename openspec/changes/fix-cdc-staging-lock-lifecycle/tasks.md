# Tasks: fix-cdc-staging-lock-lifecycle

Mechanism: **Option A** — hold the NAR download lock through the eager-CDC chunking window so cross-pod readers contend and engage the already-tested staging path (see design.md Decision Log). Implement via TDD (red → green → refactor). All new tests call `t.Parallel()`, live in `_test`/internal packages as appropriate, and run under the race detector. Run `task fmt`, `task lint`, `task test` before marking the change done.

## 1. Pre-flight (resolved during design)

- [x] 1.1 Prerequisite stack (#1374–#1376) merged into base: confirmed on `main` (`narServability`/`hasFinishedNar`, `markStagingRequested`, `stagingServeReady`, GetNar compressed-request gate all present).
- [x] 1.2 Confirm eager temp file + producer survive the whole chunking window: `ds.assetPath` removed only after `ds.cdcWg` (cache.go:3146-3162); `waitForStagingRequest` polls until `ds.done`, which closes only after chunking for eager (cache.go:3227). Recorded in design Decision Log. (No producer-liveness code needed — D2.)

## 2. Red — failing test for lock lifetime

- [x] 2.1 Added `TestCoordinateDownload_HoldsNARLockThroughChunking` (pkg/cache/coordinate_download_lock_lifetime_internal_test.go): drives `coordinateDownload` (NAR path) with `ds.stored`/`ds.done` independently and asserts the `download:nar` lock stays held after `ds.stored` and releases only after `ds.done`. Confirmed RED against the original `ds.stored`-or-`ds.done` release (lock went free at `ds.stored`).

## 3. Green — hold the download lock through chunking (D1)

- [x] 3.1 In `coordinateDownload`'s `waitForStorage == false` background release goroutine, wait on `<-ds.done` only (removed the `<-ds.stored` case) and expanded the comment to explain the eager-CDC chunking-window rationale and the non-CDC/lazy no-op. Test now GREEN.
- [x] 3.2 Non-CDC / lazy timing unchanged: the same test's "released after `ds.done`" assertion covers the path where `ds.done` closes together with `ds.stored`; the full `pkg/cache` suite (which exercises real non-CDC/lazy downloads) passes under `-race`.

## 4. Producer liveness — verify, no new code (D2)

- [x] 4.1 Added `TestStageInflightNar_ActivatesDuringChunkingWindow` (pkg/cache/inflight_staging_chunking_window_internal_test.go): a staging request recorded AFTER `ds.stored` is still observed by the producer and staged from the temp file (parts advance, status `complete`, bytes reassemble). Passes with no producer code change — confirms the `ds.done`-bounded wait already covers the chunking window.

## 5. Cross-pod coordination behavior (covered by existing + e2e)

- [x] 5.1 The "contend → `pollForDownloadOrTakeOver` → `markStagingRequested` → serve from staging" path is already shipped and tested by #1374/#1375; with D1 holding the lock through chunking, a mid-chunking cross-pod reader takes exactly that path. The unit pieces (lock held: 2.1; producer stages a mid-chunking request: 4.1) compose it; the full cross-pod composition is the e2e (6.1).
- [x] 5.2 Dead-orphan recovery untouched: D1 changes only download-lock release timing for a live eager chunk; `narServability`/#1230 logic is unmodified and its tests pass in the full suite.

## 6. End-to-end

- [ ] 6.1 Re-enable / extend the chunking-window e2e (`dev-scripts/test-cdc-lifecycle-e2e.py` and/or `test-inflight-staging-contention-e2e.py`) for 2 replicas + Redis locker + eager CDC, with a slow-chunk knob: a cross-pod compressed request mid-chunking serves HTTP 200 from staging, no 404. Green on both local and S3 backends.
- [ ] 6.2 Run the download-window contention e2e to confirm no regression in the already-green pre-chunking path.

## 7. Finalize

- [ ] 7.1 `task fmt`, `task lint`, `task test` all exit 0 under the race detector.
- [ ] 7.2 Add a `CHANGELOG.md` [Unreleased] entry for the CDC chunking-window cross-pod 404 fix.
- [ ] 7.3 Update docs only if operator-visible behavior changed (expected: none beyond existing inflight-staging docs); otherwise note "no doc change required".
- [ ] 7.4 Update memory `project_cdc_staging_lock_lifecycle_change` with the final mechanism (Option A) and archive the change (`/opsx:archive`).
