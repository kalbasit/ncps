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

## 5b. Guard A — compressed request from uncompressed staging (added after the e2e)

- [x] 5b.1 `serveNarFromStaging` gained a `requested` compression param + `compressedRequestNeedsUpstreamFallback` helper: returns `storage.ErrNotFound` when a compressed variant is requested but staging holds uncompressed bytes (eager-CDC), so the client falls back to upstream instead of being served a mislabeled body. Tests: `TestServeNarFromStaging_CompressedRequestFromUncompressedStagingFallsBack`. Commit `d77881bc`.

## 6. Out of scope — deferred to the narinfo-`none` root fix

Two follow-ups are intentionally **not** part of this scoped change; both are
captured in memory `project_cdc_narinfo_none_root_fix` for a fresh session:

- The contention e2e (`--window chunking`, xz upstream) expects the `.nar.xz`
  request served from staging — unachievable for eager CDC (in-flight bytes are
  uncompressed; ncps has no NAR compressor). The e2e's chunking-window expectation
  needs redesigning (test a none-upstream NAR for staging-serves-uncompressed;
  expect compressed→fallback) — addressed by the root fix.
- The same-pod temp path has the identical pre-existing corruption; a guard there
  was reverted because it breaks the deliberate `cdc_test.go:676`. The root fix
  (advertise narinfo `none` so `.nar.xz` is never requested) subsumes it.

## 7. Finalize

- [x] 7.1 `task fmt`, `task lint`, unit `task test` all green under `-race` (the e2e is a separate opt-in dev tool, not in `nix flake check`).
- [x] 7.2 `CHANGELOG.md` [Unreleased] entry added (corrected to: uncompressed cross-pod served from staging; compressed mid-chunking falls back to upstream).
- [x] 7.3 No doc change required (behavior-only, behind the existing inflight-staging flag).
- [x] 7.4 Memory updated: `project_cdc_staging_lock_lifecycle_change` (implementation) + `project_cdc_narinfo_none_root_fix` (deferred root fix). Archive after review/merge.
