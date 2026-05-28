## Why

`GetNar`'s per-client streaming goroutine contains at least one blocking point that does not have a cancellation escape: the `select { case <-ds.stored: case <-ds.done: }` wait and, more subtly, the `cond.Wait()` loop that only checks `downloadError` — not client context cancellation. When client A cancels mid-download, its pipe reader is closed, but the shared `downloadState` may be left in a state where the signals client B's goroutine depends on (`ds.stored`, `ds.done`) arrive in a pathological order relative to the goroutine's execution point, causing `io.ReadAll(readerB)` to block indefinitely. PR #1257 added a 30-second timeout guard with goroutine dump to convert this from a 20-minute outer-timeout hang into a fast-failing diagnostic; the root cause in production code is still present.

## What Changes

- Harden the streaming goroutine in `pkg/cache/cache.go` (`GetNar`, lines ~1175–1317) so that no blocking point can stall indefinitely when a sibling client has cancelled its context:
  - Propagate the client's own context into the `cond.Wait()` loop by waking the goroutine on `ctx.Done()` (via a side goroutine that broadcasts when the client context is cancelled).
  - Add `case <-ctx.Done():` to the `select { case <-ds.stored: case <-ds.done: }` wait so a cancelled client's goroutine does not block cleanup.
- If TDD reproduction surfaces a deeper shared-state bug in the download-coordination path (the pipe-fanout or `bytesWritten`/`finalSize` visibility), fix that too.
- Close GitHub issue #1252 once the root cause is confirmed fixed by a reliably passing stress run.

## Capabilities

### New Capabilities

_(none — this is a correctness fix to an existing streaming path)_

### Modified Capabilities

- `narinfo-concurrent-fetch`: The concurrent-fetch coordination model is adjacent; this change tightens the NAR streaming analogue but does not alter narinfo requirements.

> **Note:** No dedicated spec exists for concurrent NAR streaming today. If the investigation reveals a spec-level invariant (e.g. "when client A cancels, client B must still receive all bytes and observe `HasNar → true`"), a new `nar-concurrent-streaming` spec should be created to capture it.

## Impact

- **`pkg/cache/cache.go`**: streaming SafeGo goroutine, `cond.Wait()` loop, and the `ds.stored`/`ds.done` select (lines ~1235–1316).
- **`pkg/cache/cache_test.go`**: `testConcurrentDownloadCancelOneClientOthersContinue` — no test logic changes needed (defensive guards from #1257 stay); may add a stress harness sub-loop if repro is needed.
- **No API surface changes**, no migration, no schema changes.
- **I/O / memory**: negligible — one extra goroutine per active streaming client to watch `ctx.Done()` and broadcast; this goroutine exits immediately on broadcast.
