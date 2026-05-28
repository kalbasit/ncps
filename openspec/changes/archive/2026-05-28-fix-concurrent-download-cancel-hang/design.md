## Context

`GetNar` deduplicates in-flight downloads via a shared `downloadState` (`ds`). Each caller
gets its own `io.Pipe` and a per-client SafeGo streaming goroutine that tails the temp file and
forwards bytes. The goroutine has two blocking points:

1. **`cond.Wait()` loop** (`cache.go:~1240`): sleeps until `ds.bytesWritten` advances or
   `ds.finalSize` is set. Exits on `downloadError` but **not** on the client's own context
   cancellation.
2. **Storage-wait `select`** (`cache.go:~1301`): `select { case <-ds.stored: case <-ds.done: }`.
   Neither channel is closed until `pullNarIntoStore` completes storage and returns. If storage
   stalls (e.g., I/O pressure on a busy CI runner), neither channel fires and the goroutine
   blocks indefinitely — leaving `defer writer.Close()` un-run, so `io.ReadAll(readerB)` hangs.

PR #1257 added a 30-second `runWithTimeout` guard with goroutine dump around the two
previously un-guarded blocking calls in the test. The test now fails fast with diagnostics
instead of consuming the full 20-minute `go test` timeout.

## Goals / Non-Goals

**Goals:**
- Ensure the streaming goroutine for any client always terminates within a bounded time,
  regardless of whether a sibling client cancels or storage stalls.
- Make context cancellation an explicit exit condition in the `cond.Wait()` loop so cancelled
  clients clean up promptly without depending on downstream broadcasts.
- Preserve the correctness invariant: `HasNar` returns true before the streaming goroutine
  closes the pipe (needed because the test — and HTTP handlers — may check storage immediately
  after reading).

**Non-Goals:**
- Removing or redesigning the `downloadState` fanout model.
- Changing storage backends or adding retry logic for failed storage writes.
- Fixing unrelated flaky tests.

## Decisions

### Decision 1: Make `cond.Wait()` context-aware via a watcher goroutine

**Choice**: Launch a single short-lived goroutine per streaming goroutine that calls
`ds.cond.Broadcast()` when the client's context is cancelled.

```go
// Inside the streaming SafeGo, before the loop:
watcherDone := make(chan struct{})
defer close(watcherDone)
go func() {
    select {
    case <-ctx.Done():
        ds.cond.Broadcast()
    case <-watcherDone:
    }
}()
```

Then add `ctx.Err() != nil` to the `downloadError` check inside the loop:
```go
if ds.downloadError != nil || ctx.Err() != nil {
    ds.mu.Unlock()
    return
}
```

**Why not `sync.Cond` with a deadline?** Go's `sync.Cond` has no timeout/context API. The
watcher-goroutine pattern is the idiomatic alternative; it adds exactly one goroutine per
active streaming client, which exits immediately when either the client context cancels or the
streaming goroutine itself finishes.

**Why not poll `ctx.Err()` inside the loop?** Polling would only catch the cancellation after
the next broadcast. The watcher guarantees cancellation is noticed within microseconds by
forcing a broadcast.

### Decision 2: Add `ctx.Done()` to the storage-wait `select`

**Choice**: Extend the storage-wait select to include the client's context:

```go
select {
case <-ds.stored:
    // storage complete — normal path
case <-ds.done:
    // download done (may include error)
    if err := ds.getError(); err != nil { ... }
case <-ctx.Done():
    // client cancelled — exit cleanly without waiting for storage
    zerolog.Ctx(ctx).Debug().Msg("client context cancelled while waiting for NAR storage")
}
```

**Why this is safe**: The client has already received all bytes at this point (the loop ran to
`bytesSent >= ds.finalSize`). The only reason to wait for `ds.stored` is to satisfy the
invariant that `HasNar` returns true before the HTTP response ends. For a cancelled client,
the HTTP connection is already torn down, so the invariant doesn't apply. For a normal client,
`ds.stored` fires first (before `ds.done`, which is deferred), so behaviour is unchanged.

**Risk**: If a future caller relies on `GetNar` completing only after storage is guaranteed,
this change subtly relaxes that guarantee for cancelled contexts. Accepted: cancelled HTTP
clients don't receive a response anyway, so a HasNar race for them is inconsequential.

### Decision 3: Use TDD — write a stress reproduction harness before fixing

**Choice**: Before touching production code, write a subtest loop that runs
`ConcurrentDownloadCancelOneClientOthersContinue` 50× in a tight loop under `-race`, to get a
reproducible failure on the existing code and confirm the fix eliminates it.

**Why**: The bug has been observed only once in CI. Without a reproducible failure, we cannot
confirm the fix works. The 30-second `runWithTimeout` guard means each failed iteration fails
fast, so 50 iterations at most cost 25 minutes if all hang — acceptable for a targeted run.

## Risks / Trade-offs

- **Watcher goroutine leak**: If the watcher goroutine and the streaming goroutine both exit
  at nearly the same time, the `close(watcherDone)` in the defer ensures the watcher exits.
  No leak possible.
- **Spurious broadcast on normal exit**: When `watcherDone` closes (streaming goroutine done),
  the watcher exits via the `<-watcherDone` case — no spurious `Broadcast()` is emitted.
  Goroutines already past `cond.Wait()` are unaffected.
- **`ctx.Done()` path returns without confirming storage**: For cancelled clients only. The
  test's `HasNar` assertion runs with `ctxB` (never cancelled), so the test-level invariant
  is preserved.
- **Stress loop may not reproduce**: The bug is environment-sensitive. If the stress loop
  doesn't reproduce, fall back to shipping the defensive fixes (Decisions 1 & 2) anyway, since
  they are strictly safer than the status quo.

## Migration Plan

1. Write the stress sub-loop test (red phase — should hang on current code, then fail fast via `runWithTimeout`).
2. Apply Decision 1 (context watcher in `cond.Wait()` loop) and Decision 2 (`ctx.Done()` in storage-wait select).
3. Re-run the stress loop to confirm no hangs.
4. Run `task test` and `task lint` to confirm no regressions.
5. Ship. No migration, no schema change, no config change needed.

## Open Questions

- **Is the storage-wait `select` actually the hang site?** Without a goroutine dump from a live hang, we are reasoning from code. The `runWithTimeout` guards in the test will capture a dump the next time it fires in CI — if the fix in this PR doesn't prevent the hang, the dump will tell us exactly where to look.
- **Should the `cond.Wait()` loop also check `ds.done` directly?** If `ds.done` is closed (download goroutine exited) while the streaming goroutine is in `cond.Wait()`, the goroutine is woken by the final `Broadcast()` at line 2569. No separate check needed.
