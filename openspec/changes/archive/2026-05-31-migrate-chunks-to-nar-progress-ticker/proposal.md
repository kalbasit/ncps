## Why

`migrate-chunks-to-nar` is a long-running command that processes chunked NARs concurrently, but emits only a final summary log. Operators have no visibility into migration progress while it runs, unlike `migrate-nar-to-chunks` which already logs progress every 5 seconds.

## What Changes

- Add a `time.NewTicker(5 * time.Second)` progress-reporter goroutine to `migrateChunksToNarAction`, identical in structure to the one in `migrateNarToChunksCommand`.
- The ticker logs: `total`, `processed`, `succeeded`, `failed`, `skipped`, `percent`, `elapsed`, and `rate` fields via zerolog at Info level.
- The goroutine is started after `startTime` is set and stopped via a `progressDone` channel on `defer close(progressDone)` before `g.Wait()` returns.

## Non-goals

- No new CLI flags (interval is hardcoded at 5 s, same as `migrate-nar-to-chunks`).
- No changes to the final summary log line.
- No changes to any other migration commands.

## Capabilities

### New Capabilities
- `migrate-chunks-to-nar-progress-reporting`: Periodic (5 s) structured log lines during `migrate-chunks-to-nar` execution reporting total, processed, succeeded, failed, skipped, completion percentage, elapsed time, and processing rate.

### Modified Capabilities
<!-- none -->

## Impact

- **Code**: `pkg/ncps/migrate_chunks_to_nar.go` — `migrateChunksToNarAction` only.
- **I/O / memory**: Negligible — one goroutine, one ticker, four `atomic.LoadInt32` calls every 5 seconds.
- **Network / latency**: None.
