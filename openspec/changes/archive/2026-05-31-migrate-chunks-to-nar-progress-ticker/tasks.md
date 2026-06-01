## 1. Tests (TDD — write first)

- [x] 1.1 In `pkg/ncps/migrate_chunks_to_nar_test.go`, add a test that runs `migrateChunksToNarAction` with several chunked NARs, captures zerolog output, and asserts that at least one "migration progress" log line is emitted with the expected fields (`total`, `processed`, `succeeded`, `failed`, `skipped`, `percent`, `elapsed`, `rate`)
- [x] 1.2 Add a test asserting no "migration progress" line is emitted when `chunkedCount == 0` (early-return path)
- [x] 1.3 Confirm both tests fail (red) before touching the implementation

## 2. Implementation

- [x] 2.1 In `pkg/ncps/migrate_chunks_to_nar.go`, after `startTime := time.Now()`, add `progressTicker := time.NewTicker(5 * time.Second)` and `defer progressTicker.Stop()`
- [x] 2.2 Add `progressDone := make(chan struct{})` and `defer close(progressDone)` immediately after the ticker
- [x] 2.3 Start the progress goroutine (select on `progressTicker.C` / `progressDone`) that reads the four atomic counters and logs `total`, `processed`, `succeeded`, `failed`, `skipped`, `percent`, `elapsed`, `rate` at Info level with message `"migration progress"` — matching the field names and formatting in `migrate_nar_to_chunks.go`
- [x] 2.4 Confirm both tests pass (green)

## 3. Verification

- [x] 3.1 Run `task fmt` and confirm zero diff
- [x] 3.2 Run `task lint` and confirm zero findings
- [x] 3.3 Run `task test` and confirm all tests pass
