## ADDED Requirements

### Requirement: Concurrent NAR streaming goroutines MUST terminate within a bounded time

When multiple clients concurrently request the same NAR hash and one client's context is
cancelled mid-download, the streaming goroutine for the cancelled client SHALL exit promptly
(without waiting for the next `cond.Broadcast()`) and the streaming goroutines for all
remaining clients SHALL continue to completion unaffected.

#### Scenario: Cancelled client's streaming goroutine exits on context cancellation

- **WHEN** client A and client B both have active streaming goroutines for the same NAR hash
- **AND** client A's context is cancelled while its goroutine is blocked in the `cond.Wait()` loop
- **THEN** client A's streaming goroutine MUST exit within a bounded time (≤ a few milliseconds after cancellation), without requiring a broadcast from the download goroutine
- **AND** the per-client `io.Pipe` writer for client A MUST be closed so the caller's reader unblocks

#### Scenario: Non-cancelled client receives all bytes after sibling cancellation

- **WHEN** client A and client B concurrently call `GetNar` for the same NAR hash
- **AND** client A cancels its context mid-download
- **AND** client B's context is never cancelled
- **THEN** client B's reader SHALL receive all bytes of the NAR content
- **AND** `io.ReadAll` on client B's reader SHALL complete without hanging
- **AND** `HasNar` SHALL return true after client B's read completes

#### Scenario: Streaming goroutine exits cleanly when storage stalls for a cancelled client

- **WHEN** a client's context is cancelled
- **AND** the streaming goroutine has finished forwarding all bytes to the client pipe
- **AND** the storage goroutine (`storeNarFromTempFile`) has not yet completed
- **THEN** the streaming goroutine for the cancelled client SHALL exit via the `ctx.Done()` case
- **AND** `defer writer.Close()` SHALL run, unblocking the caller
- **AND** the storage goroutine SHALL continue to completion independently

### Requirement: Streaming goroutine `cond.Wait()` loop MUST check client context cancellation

The `cond.Wait()` loop in the per-client streaming goroutine SHALL treat client context
cancellation as an exit condition equivalent to `downloadError`, so that a cancelled client
does not hold a streaming goroutine open indefinitely waiting for a `Broadcast()` that may
arrive infrequently.

#### Scenario: Context-cancelled client exits loop without broadcast

- **WHEN** the streaming goroutine is blocked in `cond.Wait()` (no new bytes available, download not complete)
- **AND** the client's context is cancelled
- **THEN** the goroutine SHALL wake within microseconds (via a watcher goroutine that calls `ds.cond.Broadcast()` on `ctx.Done()`)
- **AND** the goroutine SHALL check `ctx.Err() != nil` and return, closing the pipe writer

### Requirement: Storage-wait `select` MUST include a client context escape

After the streaming goroutine has forwarded all bytes, it waits for `ds.stored` or `ds.done`
before closing the pipe. This `select` SHALL also include `ctx.Done()` so that a cancelled
client does not remain blocked if the storage operation stalls.

#### Scenario: Cancelled client does not block at storage-wait select

- **WHEN** the streaming goroutine has sent all bytes (`bytesSent >= ds.finalSize`)
- **AND** neither `ds.stored` nor `ds.done` has fired yet (storage is in progress)
- **AND** the client's context is cancelled
- **THEN** the goroutine SHALL exit via `<-ctx.Done()`
- **AND** the pipe writer SHALL be closed, allowing the caller's `io.ReadAll` to return

#### Scenario: Non-cancelled client still waits for storage confirmation

- **WHEN** the streaming goroutine has sent all bytes
- **AND** the client's context is active (not cancelled)
- **THEN** the goroutine SHALL wait for `ds.stored` or `ds.done` before closing the pipe
- **AND** `HasNar` SHALL return true when observed by the caller after reading completes
