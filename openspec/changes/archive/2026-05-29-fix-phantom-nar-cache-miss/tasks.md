## Status (verify-first outcome)

Implementation used a verify-first TDD pass. Key finding: the prior fix PRs
(#1255–#1290) had **already** landed most of the read-path machinery — placeholder
rows route to download in both `GetNar` and `prePullNar`, `chunking_started_at` is
cleared on chunking failure, `streamProgressiveChunks` already errors on aborted
chunking, a CDC lazy-recovery job exists, and the server handler maps both
`storage.ErrNotFound` and `upstream.ErrNotFound` to HTTP 404.

What this change therefore actually did:
- **D5 (real gap, fixed):** upstream retry now also covers `http2: timeout awaiting
  response headers`, connection resets, and broken pipe — not just `GOAWAY`. Genuine
  404s remain non-retryable. New tests in `pkg/cache/upstream/transient_retry_test.go`.
- **D1 (defense-in-depth refactor):** the duplicated, drift-prone servability checks
  in `GetNar` and `prePullNar`'s `hasAsset` are unified behind one `isServable()`
  predicate, so the recurring placeholder regression cannot return via divergent
  checks. Behavior-preserving (full CDC suite + new tests green).
- **Characterization tests (regression guards):** `pkg/cache/phantom_recovery_test.go`
  reproduces the production scenario end-to-end (narinfo 200 → failed NAR download →
  recovery on next request) plus the genuine-404 path.
- **Recovery-sweep fix:** the CDC lazy-recovery job now skips backing-less stuck rows
  (only re-drives NARs that have a whole-file in the store and can actually be chunked),
  ending the indefinite re-drive of unmigratable / genuinely-absent hashes.

Per `.claude/rules` (no-duplicate-tests guidance), behaviors already proven by the
existing CDC suite were not re-tested with redundant unit tests.

## 1. Reproduce & characterize the defect (TDD)

- [x] 1.1 Characterization test: backing-less row (`total_chunks=0`, `chunking_started_at` NULL) → `GetNar` re-downloads instead of 404. (`testCDCBackingLessRecordRecoversAfterTransientFailure`; passed — behavior already correct.)
- [x] 1.2 `hasAsset` placeholder behavior — covered by the recovery test + `isServable` unit path; no separate redundant internal test added.
- [x] 1.3 `streamProgressiveChunks` short-body — already errors on aborted chunking (cache.go ~L7088); placeholders no longer reach it after the `isServable` routing. Covered, no redundant test added.
- [x] 1.4 Established baseline: the recovery/404 characterization tests were green on current code (verify-first), so the remaining work was the D5 gap + D1 refactor, not a from-scratch fix.

## 2. Single source of truth for servability (D1)

- [x] 2.1 Added `isServable(ctx, narURL)` in `pkg/cache/cache.go` (whole-file in store, OR `total_chunks>0`, OR chunking active within `cdcChunkingLockTTL`) + `narFileServable(nr)` record predicate.
- [x] 2.2 Servability states exercised by the recovery test, genuine-404 test, and the existing CDC suite; no redundant dedicated unit test added (per no-duplicate-tests guidance).
- [x] 2.3 Routed `GetNar`'s servability decision through `isServable` (preserving the active-local-job temp-file-streaming nuance).
- [x] 2.4 Routed `prePullNar`/`coordinateDownload` `hasAsset` through `isServable`.

## 3. Backing-less record ⇒ cache miss ⇒ synchronous re-download (D2)

- [x] 3.1 `GetNar` falls through to prePull/coordinateDownload for backing-less rows (never short-circuits to `ErrNotFound`). Verified + guarded by the recovery test.
- [x] 3.2 Successful re-download streams full bytes AND heals the record; subsequent `GetNar` served from cache. (Asserted in the recovery test.)
- [x] 3.3 Genuine upstream 404 surfaces a not-found sentinel (→ HTTP 404) without persisting a blocking record. (`testCDCBackingLessRecordGenuine404ReturnsNotFound`.)

## 4. Streaming integrity — fail loud, never short-200 (D4)

- [x] 4.1 No-chunks-will-arrive case: placeholders no longer enter `getNarFromChunks` (routed to download by `isServable`); confirmed by the recovery test.
- [x] 4.2 `streamProgressiveChunks` already surfaces an error when chunking is aborted/stalled (cache.go ~L7088) rather than closing a short body. Verified, unchanged.
- [x] 4.3 Aborted/stalled producer → error not truncated 200: existing behavior verified; no redundant test added.

## 5. Don't persist authoritative placeholders (D3)

- [x] 5.1 Audited `storeInDatabase`/`createOrUpdateNarFileEnt`: the narinfo→nar_file link, `last_accessed_at`, and `file_size` rely on the row existing; the chosen direction is to keep the row but make its existence non-servable via `isServable`.
- [x] 5.2 Implemented via `isServable` (D1). No schema migration required.
- [x] 5.3 Failed NAR download leaves the system able to re-download on the next `GET /nar`. (Recovery test.)

## 6. Stuck-record recovery / self-heal (cdc-chunking + recovery sweep)

- [x] 6.1 Stuck rows (stale `chunking_started_at`) are non-servable via `narFileServable` (TTL check) → `GetNar` re-drives the download.
- [x] 6.2 The CDC lazy-recovery job now skips backing-less stuck rows (no whole-file in store) — `BackgroundMigrateNarToChunks` can only chunk a store-present NAR, so re-driving placeholder/genuinely-absent rows every interval was futile ("error fetching nar from store: not found" spam) and indefinitely retried unmigratable hashes. Such rows are recovered on demand by `GetNar`. Gated in `runCDCLazyRecovery` on `HasNarInStore`; test `TestRunCDCLazyRecoverySkipsBackingLessRows`.

## 7. Upstream resilience (D5)

- [x] 7.1 Retry now covers `http2: timeout awaiting response headers`, connection reset, and broken pipe via `isRetriableTransportError`; genuine 404 stays non-retryable.
- [x] 7.2 A transient failure leaves no poisoning record and is retryable on the next request. (Retry tests + recovery test.)

## 8. Server handler & integration coverage

- [x] 8.1 Verified `pkg/server/server.go` nar handler maps both `storage.ErrNotFound` and `upstream.ErrNotFound` to HTTP 404, else 500 — correct for the new behavior.
- [x] 8.2 The production scenario (narinfo 200 → failed download → recovery serving a full, non-truncated body) is reproduced at the cache layer in `phantom_recovery_test.go`; a separate server-HTTP duplicate was not added.

## 9. Verify & finalize

- [x] 9.1 `task fmt` exits zero (0 files changed).
- [x] 9.2 Changed files lint clean (package-scoped `golangci-lint`). NOTE: bare `task lint` in this story worktree reports pre-existing issues from sibling checkouts (`../../ncps-refactors-improvements`, `../../../repositories`) — zero in this worktree; environmental, not from this change.
- [x] 9.3 `task test` passes (all packages, race detector on the cache/upstream suites).
