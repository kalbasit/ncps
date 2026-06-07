## 1. Drop the age gate; rely on lock liveness (Comment B)

- [x] 1.1 Write failing test: a fresh in-progress orphan (`total_chunks=0`, recent
      `chunking_started_at`, partial chunks) with a free `migrationLockKey(hash)` is reaped
      by `runCDCLazyRecovery` in one pass — partial `nar_file_chunks` deleted,
      `chunking_started_at` cleared, `total_chunks` left 0.
      (`TestRunCDCLazyRecoveryReapsFreshInProgressOrphanWithFreeLock`)
- [x] 1.2 Existing test already covers an in-progress orphan whose `migrationLockKey(hash)`
      is held (simulated peer) being skipped
      (`TestRunCDCLazyRecoverySkipsStaleCDCChunkingRowWhenMigrationLockHeld`); the old
      "fresh untouched" test (encoding the removed age-gate) was replaced with
      `TestRunCDCLazyRecoveryLeavesCompletedChunkingRowUntouched`.
- [x] 1.3 Remove the `entnarfile.ChunkingStartedAtLT(staleCutoffTime)` predicate from the
      in-progress branch of the recovery candidate query in `runCDCLazyRecovery`; drop
      the unused `staleCutoffTime`. ALSO removed the redundant in-transaction age gate in
      `clearStaleCDCChunkingLockWithEntTx` (line 2479) — the read-path caller pre-gates on
      age, recovery gates on the migration-lock TryLock.
- [x] 1.4 Correct the recovery doc comment to state the migration-lock liveness premise.
- [x] 1.5 Run `task test` (SQLite suite) green.

## 2. Benign ErrMigrationInProgress in background chunking (Comment C)

- [x] 2.1 Failing tests: when background CDC chunking returns `ErrMigrationInProgress`,
      no error-level log is emitted and `downloadState.setError` is not called; a genuine
      error still fails the download. (`TestReportBackgroundCDCError*`)
- [x] 2.2 Added `reportBackgroundCDCError` helper branching on
      `errors.Is(cdcErr, ErrMigrationInProgress)` (debug log, no `setError`); wired both
      the pipe path and the simple-path callers to it.
- [x] 2.3 Run `task test` green.

## 3. Disambiguate recovery skip counters (Comment D)

- [x] 3.1 Failing test asserting a lazy-disabled skip increments
      `lazy_chunking_disabled_skip_count` and NOT `stale_recovery_skip_count`.
      (`TestRunCDCLazyRecoveryLazyDisabledSkipUsesDistinctCounter`)
- [x] 3.2 Added `lazyChunkingDisabledSkipCount`, incremented at the lazy-disabled branch,
      emitted as a separate field in the completion log.
- [x] 3.3 Run `task test` green.

## 4. Read-path liveness gate — the actual #1230 symptom fix (Comment B follow-through)

- [x] 4a.1 Failing tests: `isServable` returns false for a fresh in-progress orphan with a
      FREE migration lock, true when the lock is HELD (live producer), true when fully
      chunked. (`TestIsServable*` in `read_path_liveness_internal_test.go`)
- [x] 4a.2 Add `cdcChunkerLive` (non-blocking `TryLock`+`Unlock` probe, fail-safe to live)
      and gate `isServable` on it for the `total_chunks==0 && fresh-lock` case so `GetNar`
      re-downloads instead of streaming partial chunks + stalling.
- [x] 4a.3 Failing test: under the migration lock, `findOrCreateNarFileForCDC` takes over a
      fresh orphan instead of `ErrAlreadyExists`.
      (`cdc_chunker_takeover_internal_test.go`)
- [x] 4a.4 Remove the `age < cdcChunkingLockTTL → ErrAlreadyExists` gate in
      `findOrCreateNarFileForCDC`; always reclaim under the lock.
- [x] 4a.5 Update the two progressive-streaming tests to hold the migration lock (via a new
      `AcquireMigrationLockForTest` export seam) so they faithfully simulate a live chunker.

## 5. Verify

- [x] 4.1 `task fmt`, `task lint`, `task test` all exit 0.
- [x] 4.2 `openspec validate cdc-orphan-chunking-recovery --strict` passes.
- [x] 4.3 Posted a maintainer summary comment on PR #1317 covering B/C/D (and the read/write
      liveness follow-through); updated the PR title/description to match the implementation.
