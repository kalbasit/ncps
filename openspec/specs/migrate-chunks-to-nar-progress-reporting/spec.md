# Spec: migrate-chunks-to-nar-progress-reporting

## Purpose

Defines the progress reporting behaviour of the `migrate-chunks-to-nar` command. While the migration is running, the command periodically emits structured log lines so operators can observe throughput, completion percentage, and error counts without tailing raw output.

## Requirements

### Requirement: Periodic progress log during migration

While `migrate-chunks-to-nar` is running, the command MUST emit a structured log line at the Info level every 5 seconds reporting migration progress until the errgroup finishes.

#### Scenario: Progress line emitted while workers are active
- **WHEN** the `migrate-chunks-to-nar` command is processing chunked NARs and at least 5 seconds have elapsed since start
- **THEN** a zerolog Info line is emitted with fields: `total` (int64, total chunked NARs at query time), `processed` (int32), `succeeded` (int32), `failed` (int32), `skipped` (int32), `percent` (string, e.g. `"42.00%"`), `elapsed` (string, rounded to second), `rate` (float64, processed/second), and message `"migration progress"`

#### Scenario: Progress goroutine exits before final summary
- **WHEN** `g.Wait()` returns (all workers done, whether success or failure)
- **THEN** the progress goroutine has already exited (via `progressDone` channel close) before the final summary log line is written

#### Scenario: Zero-item migration emits no spurious progress lines
- **WHEN** `migrate-chunks-to-nar` is invoked and `chunkedCount == 0`
- **THEN** the command returns early before the progress goroutine is started; no "migration progress" log lines are emitted

#### Scenario: Rate is zero for sub-second runs
- **WHEN** the elapsed duration is zero or near-zero when the ticker fires
- **THEN** `rate` is reported as `0` (no division by zero)

#### Scenario: Percent is zero when total is zero
- **WHEN** `totalToChunk` is `0` when the ticker fires (should not happen in normal flow, but guard is present)
- **THEN** `percent` is reported as `"0.00%"` (no division by zero)
