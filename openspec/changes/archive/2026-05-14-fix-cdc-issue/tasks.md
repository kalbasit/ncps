## 1. Regression Test (Failing First)

- [x] 1.1 In `pkg/cache/`, add a test helper `limitedReader` (or use `io.LimitReader`) that returns `io.EOF` after N bytes, simulating a truncated upstream stream
- [x] 1.2 Write `TestStoreNarWithCDC_TruncatedStream` that calls `storeNarWithCDCFromReader` with a reader producing fewer bytes than `narInfo.NarSize` and asserts the function returns a non-nil error
- [x] 1.3 Assert that after the call, the `nar_file` row in the DB has `total_chunks = 0` (not committed as complete)
- [x] 1.4 Verify test fails before the fix is applied (confirms it's a real regression test)

## 2. CDC Commit-Site Size Validation

- [x] 2.1 In `storeNarWithCDCFromReader` (`pkg/cache/cache.go`), locate the `if !ok { ... }` branch that fires when `chunksChan` closes
- [x] 2.2 After accumulating the final batch into `totalSize`, add a guard: `if fileSize > 0 && uint64(totalSize) != fileSize { return fmt.Errorf(..., io.ErrUnexpectedEOF) }` — do NOT call `UpdateNarFileTotalChunks`
- [x] 2.3 Verify the error message includes both `fileSize` (expected) and `totalSize` (actual) and the word "truncated"

## 3. Upgrade CDC Goroutine Error Log Level

- [x] 3.1 In `pullNarIntoStore` (`pkg/cache/cache.go`), find the goroutine started around line 2479 that calls `storeNarWithCDCFromReader`
- [x] 3.2 Change the error handler for a non-nil return from `storeNarWithCDCFromReader` to log at `error` level (not `debug`), including the narinfo hash and NAR URL
- [x] 3.3 Ensure no "download of nar complete (CDC chunking in background)" success log fires when an error is returned

## 4. New DB Query: GetCDCNarFilesWithSizeMismatch

- [x] 4.1 Add `GetCDCNarFilesWithSizeMismatch` to `db/query.sqlite.sql` — selects `nar_files` rows where `total_chunks > 0` AND `file_size != narinfos.nar_size`, joined via `narinfo_nar_files`
- [x] 4.2 Add the equivalent query to `db/query.postgres.sql`
- [x] 4.3 Add the equivalent query to `db/query.mysql.sql`
- [x] 4.4 Run `sqlc generate` to produce the Go query wrappers
- [x] 4.5 Run `go generate ./pkg/database` to update the database wrapper interfaces

## 5. Extend Fsck to Use the New Query

- [x] 5.1 Add `narFilesWithSizeMismatch []database.NarFile` field to `fsckResults` struct in `pkg/ncps/fsck.go`
- [x] 5.2 Include `len(r.narFilesWithSizeMismatch)` in the `totalIssues()` method
- [x] 5.3 In `collectFsckSuspects`, add a phase (after 1f) that calls `db.GetCDCNarFilesWithSizeMismatch` and populates `results.narFilesWithSizeMismatch` (only when `cdcMode` is true)
- [x] 5.4 In `reVerifyFsckSuspects`, re-verify each suspect by re-running the size comparison (re-query or compare `nf.FileSize` vs joined `narinfos.nar_size` for that `nar_file_id`)
- [x] 5.5 In `printFsckSummary`, add row `"CDC NARs w/ size mismatch:"` inside the `if r.cdcMode { ... }` block
- [x] 5.6 In `repairFsckIssues`, add a section that calls `repairBrokenCDCNarFiles` (or equivalent) for `results.narFilesWithSizeMismatch`

## 6. Tests for Fsck Size-Mismatch Detection

- [x] 6.1 In `pkg/ncps/fsck_test.go`, add a test that inserts a `nar_file` with `total_chunks > 0` and `file_size` smaller than the linked narinfo's `nar_size`, then asserts `ncps fsck` reports it under size mismatch
- [x] 6.2 Add a test that asserts `ncps fsck --repair` deletes the mismatched row and its orphaned narinfo
- [x] 6.3 Add a test that asserts correctly-chunked rows (file_size == nar_size) are NOT flagged

## 7. Lint, Format, and Final Verification

- [x] 7.1 Run `golangci-lint run --fix` and fix any remaining linter errors
- [x] 7.2 Run `nix fmt` to format all files
- [x] 7.3 Run `sqlfluff format db/query.*.sql` to format SQL files
- [x] 7.4 Run `go test -race ./pkg/cache/... ./pkg/ncps/...` (with integration test env vars set if available) and confirm all tests pass
- [x] 7.5 Confirm the regression test from task 1.2 now passes (was failing before fix)
