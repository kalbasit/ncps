# NAR Concurrent Streaming

## Purpose

Defines correctness and termination requirements for the per-client streaming goroutines that
serve NAR content to multiple concurrent clients requesting the same NAR hash. In particular,
governs how client context cancellation propagates so that cancelled clients exit promptly
without blocking non-cancelled peers.

## Requirements

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

### Requirement: A lock-losing replica MUST NOT return HTTP 500 from download coordination

A lock-losing replica MUST NOT return HTTP 500 from download coordination. When a replica fails to acquire the distributed download lock for a NAR hash (because another replica holds it), it SHALL instead serve the NAR (if the holder produces it), take over the download (if the holder finishes without producing it), or return a clean cache miss (HTTP 404) if the NAR is genuinely unavailable.

#### Scenario: Holder completes successfully — waiter serves the NAR

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A completes the download and the NAR becomes present in shared storage
- **THEN** replica B SHALL detect the asset and serve the NAR with HTTP 200
- **AND** replica B SHALL NOT return HTTP 500

#### Scenario: Holder fails and releases the lock — waiter takes over

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A's download fails (e.g. upstream stream reset) and replica A releases the lock without the asset appearing in storage
- **THEN** replica B SHALL re-acquire the download lock and perform the download itself
- **AND** replica B SHALL NOT return HTTP 500 as a result of the original lock-acquisition failure

#### Scenario: NAR genuinely absent upstream — waiter returns 404 not 500

- **WHEN** replica B fails to acquire the download lock for hash `H`
- **AND** the NAR for `H` does not exist upstream (the holder, or B after take-over, observes a 404)
- **THEN** the coordination path SHALL surface `storage.ErrNotFound`
- **AND** the server SHALL return HTTP 404
- **AND** the server SHALL NOT return HTTP 500

#### Scenario: Holder still legitimately downloading past the poll window — waiter does not 500

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A is still actively downloading a large NAR and continues to refresh its lock TTL beyond the previous fixed poll timeout
- **THEN** replica B SHALL continue waiting up to the lock TTL bound rather than returning HTTP 500
- **AND** replica B SHALL serve the NAR once it appears, or return HTTP 404 on terminal give-up — never HTTP 500

### Requirement: Lock-loss fallback MUST serialize, not start a concurrent same-hash download

A replica that loses the download lock SHALL NOT begin its own concurrent
download of the same hash while another replica still holds the lock. It SHALL
wait for the holder's terminal state and only download after successfully
re-acquiring the lock, guaranteeing at most one active downloader per hash.

#### Scenario: Waiter does not download while holder still holds the lock

- **WHEN** replica B fails to acquire the download lock for hash `H` held by replica A
- **AND** replica A still holds the lock (download in progress)
- **THEN** replica B SHALL poll for the asset and re-attempt lock acquisition, but SHALL NOT start fetching `H` from upstream
- **AND** at most one replica SHALL be actively downloading hash `H` at any time

#### Scenario: Serialized take-over avoids concurrent CDC chunking

- **WHEN** CDC is enabled and replica B takes over a download for hash `H` after replica A released the lock
- **THEN** only replica B SHALL chunk hash `H` at that time
- **AND** the fix SHALL NOT introduce concurrent chunking of the same hash across replicas

### Requirement: Progressive chunk streaming MUST NOT deliver a truncated NAR body

When serving a NAR via progressive CDC streaming (`streamProgressiveChunks`/`getNarFromChunks`),
the system SHALL NOT emit a successful (HTTP 200) response whose body is shorter than the NAR's
declared size. If chunks stop arriving before the full NAR is produced — because chunking was
aborted, stalled past the lock TTL, or the producing download failed — the streaming path SHALL
surface an error (so the client sees a failed transfer / retryable condition) rather than closing
a short, well-formed-looking body.

When the response has not yet committed bytes to the client, the path SHALL prefer falling back to
a synchronous upstream re-download over emitting a partial body.

#### Scenario: Aborted chunking does not yield a short 200

- **GIVEN** a NAR for hash `H` is being served via progressive streaming
- **AND** chunking for `H` is aborted (`total_chunks` stays 0, `chunking_started_at` becomes NULL)
  before all bytes are produced
- **WHEN** the streaming goroutine observes no further chunks
- **THEN** it SHALL surface an error on the stream
- **AND** SHALL NOT terminate the response as a successful full transfer

#### Scenario: No chunks available falls back to re-download before sending bytes

- **GIVEN** `getNarFromChunks` is entered for hash `H`
- **AND** `total_chunks = 0` and `chunking_started_at` is NULL (no chunks will arrive)
- **AND** no bytes have yet been written to the client
- **WHEN** the system resolves how to serve `H`
- **THEN** it SHALL attempt a synchronous upstream re-download of `H`
- **AND** SHALL NOT return a terminal `storage.ErrNotFound` without that attempt

#### Scenario: Stalled chunk producer is treated as failure, not completion

- **GIVEN** progressive streaming for hash `H` is waiting on the chunk producer
- **AND** the producer has made no progress for longer than `cdcChunkingLockTTL`
- **AND** the NAR is not otherwise present (no whole-file, `total_chunks = 0`)
- **WHEN** the wait elapses
- **THEN** the streaming path SHALL surface an error rather than completing the response
