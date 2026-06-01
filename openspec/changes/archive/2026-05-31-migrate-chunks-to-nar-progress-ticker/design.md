## Context

`migrate-chunks-to-nar` runs a concurrent errgroup over all chunked NAR files, which can take minutes on large deployments. The final summary log line only appears after `g.Wait()` returns. Operators running the command interactively or monitoring it in CI have no way to gauge how far along the migration is.

`migrate-nar-to-chunks` already solves this with a background goroutine that fires a zerolog Info line every 5 seconds, using four `atomic.LoadInt32` counters and a `time.NewTicker`. This design adds the identical pattern to `migrateChunksToNarAction`.

## Goals / Non-Goals

**Goals:**
- Emit a structured "migration progress" log line every 5 seconds while `migrateChunksToNarAction` is running.
- Include `total`, `processed`, `succeeded`, `failed`, `skipped`, `percent`, `elapsed`, and `rate` fields — matching the `migrate-nar-to-chunks` log schema exactly.
- Stop the goroutine cleanly before the final summary log is written.

**Non-Goals:**
- No new CLI flag to configure the interval.
- No changes to the final summary log format.
- No changes to any other command.

## Decisions

**Copy the goroutine pattern verbatim from `migrateNarToChunksCommand`.**

Alternative considered: extract a shared helper function. Rejected — the helper would need to accept ~6 atomic pointers plus a `total` int64 and a `startTime`, making the signature more complex than the inline goroutine. The pattern is stable (two migration commands only), so duplication is preferable to a premature abstraction.

**Use `defer close(progressDone)` to signal shutdown.**

The `progressDone` channel is closed via `defer` right after `progressTicker.Stop()` is deferred. Closing the channel (rather than sending a value) is correct because it unblocks the goroutine even if it is between `select` cases. This matches the existing pattern.

**Keep the `totalProcessed` increment position unchanged.**

In `migrateChunksToNarAction`, `totalProcessed` is incremented at the top of each goroutine (before per-item work). This means the progress ticker reflects items "picked up" rather than items fully completed. This is intentional — consistent with how `migrate-nar-to-chunks` counts — and avoids the ticker showing 0 for the entire duration on fast machines.

## Risks / Trade-offs

- [Minor log noise] Two extra `defer` calls and one goroutine per invocation → negligible overhead for a batch job.
- [Goroutine leak] If the errgroup context is cancelled before `progressDone` is closed, the goroutine would block on `progressTicker.C` indefinitely. Mitigation: the `defer close(progressDone)` fires when `migrateChunksToNarAction` returns regardless of whether `g.Wait()` errored, so the goroutine always exits.

## Migration Plan

No deployment steps needed. This is a pure additive change to the CLI command's runtime behaviour. Rolling back means reverting the file; there are no persistent side-effects.

## Open Questions

None.
