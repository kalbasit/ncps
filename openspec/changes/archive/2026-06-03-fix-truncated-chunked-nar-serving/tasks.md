## 1. Serving integrity — completed-path completeness check (TDD)

- [x] 1.1 RED: write a test seeding a completed `nar_file` (`total_chunks = N`) with fewer than `N` `nar_file_chunks` rows, asserting `GetNar` returns `storage.ErrNotFound` (not a reader that fails mid-stream). Run it; confirm it fails against current behavior.
- [x] 1.2 GREEN: in `getNarFromChunks`, on the `total_chunks > 0` branch, count junction links in the init transaction and return `storage.ErrNotFound` synchronously when `links != total_chunks` (before `io.Pipe`). Make 1.1 pass.
- [x] 1.3 RED→GREEN: fully-linked completed NAR (`links == total_chunks`) still streams `200` with correct bytes. Covered by the pre-existing `testCDCDrainModeGetNarServesFromChunks` / `testCDCPutAndGet`, which still pass under the new guard — no separate test added.
- [x] 1.4 RED→GREEN: `TestGetNarFromChunks_MidChunkingPartialLinksNotFalse404` asserts a progressive NAR (`total_chunks = 0`, `chunking_started_at` set) with partial links is NOT resolved to `404` (HA-safety / completion-latch invariant).

## 2. Serving integrity — no partial body before error

- [x] 2.1 `testServeCompletedNarMissingLinkReturns404` asserts `GetNar` returns `storage.ErrNotFound` **synchronously** (no reader handed back), so the handler cannot have written any body before the error.
- [x] 2.2 Confirmed `pkg/server/server.go` `getNar` (`:856`) maps `storage.ErrNotFound → 404` via `http.Error(StatusText)` (no leak) before any body write. No change needed.

## 3. Drain / migrate hardening — skip, continue, report (TDD)

- [x] 3.1/3.2 `TestMigrateChunksToNar_MissingLinkIsReportedAsMissingChunk`: a link-missing completed NAR is reported as `cache.ErrMissingChunk`. The migrate driver loop (`pkg/ncps/migrate_chunks_to_nar.go:431`) already maps `ErrMissingChunk` to `PurgeChunkedNar`+continue+count, and the serving guard (1.2) routes link-loss into that path via `MigrateChunksToNar`'s `ErrNotFound→ErrMissingChunk` mapping (`cache.go:7690`). No driver-loop code change required.
- [x] 3.1/3.2 (verify W1) `TestMigrateChunksToNar_CLI_SkipsBrokenNarMigratesRestAndReports` (`pkg/ncps`): a **mix** of one reassemblable NAR (Nar1) + one un-reassemblable NAR (Nar2, missing a link) — the good one migrates to a whole file, the broken one is purged, the run continues and exits 0.
- [x] 3.3 (verify W2) The same CLI test captures stdout and asserts the summary reports `"succeeded":1`, `"purged":1`, and the purged NAR by hash. (Per-row `nar_hash` logs at `:377`, summary counts at `:481`.)

## 4. Purge / self-heal path

- [x] 4.1 `TestPurgeChunkedNar_LeavesCleanCacheMissForRefetch`: purging an un-reassemblable NAR removes the `nar_file` (HasNarInChunks/HasNarInStore both false) while leaving the narinfo intact → a clean cache miss. (W3) The `cdc-drain-mode` spec scenario was tightened to assert exactly this precondition; the upstream-fetch-and-serve-on-miss step is the cache's general miss path, covered by existing `GetNar` tests rather than re-tested here (Nar1 is xz upstream → re-testing it would duplicate well-covered CDC/decompress integration).
- [x] 4.2 Open Question resolved: purge stays drain-only; the serve path is side-effect-free (no purge-on-404). Recorded in `design.md` (contention/storm rationale).

## 5. Verification

- [x] 5.1 `task fmt` (clean), `task lint` (0 issues), `task test` (all packages ok).
- [x] 5.2 `go test -race ./pkg/cache ./pkg/server` — both ok.
- [x] 5.3 (post-verify) Added `pkg/ncps` mix-migration CLI test (W1/W2) and tightened the W3 spec scenario; `dropLastChunkLink` helper dedupes the three seed sites (S1). Re-ran fmt/lint/test — clean.
