## Why

Under CDC with HA (replicas > 1), a cross-pod reader requesting a compressed NAR (`.nar.xz`) **while a peer is still chunking it** receives a 404. The in-flight staging feature was supposed to close this (#1289), but it never activates for the CDC chunking window: staging activation keys on download-lock *contention*, yet CDC releases the `download:nar:<hash>` lock as soon as the NAR bytes are stored (decompress done) — **before** the asynchronous chunking finishes. During that chunking window the lock is free, so a later reader *acquires* it instead of contending, never records a staging request, and falls into the post-lock servability check (`coordinateDownload`, cache.go:6466) which treats the actively-chunking row as "already in storage." The request is then served from the decompressed chunks, which cannot satisfy a compressed variant → 404. This is a lock-lifecycle/design mismatch, not an incremental bug; prior site-by-site patches (#1374–#1376) fixed the GetNar gate and the poll-loop `finished`-vs-`servable` split but cannot reach this path because the loser never contends.

## What Changes

- Close the CDC chunking-window 404 so a compressed request for an actively-chunking NAR is served (from staging or an equivalent in-flight source) or coordinated to wait — never chunk-served into a 404.
- Realign in-flight staging activation with the **chunking** lifecycle, not just the download/decompress lifecycle, so cross-pod readers reliably engage staging while a peer chunks.
- Fix the post-lock servability check (`coordinateDownload`, cache.go:6466) so an actively-chunking row is not mistaken for a finished asset — without triggering a redundant re-download or a double-chunk that conflicts with the holder's migration lock.
- The concrete mechanism (extend the lock hold through chunking · route site-6466 to staging/wait · transcode-serve from the holder's live source) is evaluated and chosen in `design.md`.
- Re-enable the chunking-window e2e (`dev-scripts/test-cdc-lifecycle-e2e.py` / `test-inflight-staging-contention-e2e.py`) as the proof-of-fix.

## Non-goals

- The download-window (pre-chunking) staging path — already fixed and green via #1374–#1376.
- The whole in-flight staging machinery (`staging_state`, part-objects, GC, takeover) — already merged; this change only corrects *when/how* it engages for CDC.
- Non-CDC and non-HA behavior; lazy-vs-eager CDC policy; the chunk format itself.
- Any new config flags, DB schema, or migrations.

## Capabilities

### New Capabilities
- (none)

### Modified Capabilities
- `inflight-nar-staging`: staging MUST activate for a cross-pod reader throughout the CDC chunking window, not only during the download/decompress window.
- `cdc-chunking`: a compressed request for an actively-chunking NAR MUST be coordinated (served from staging or made to wait), never served from decompressed chunks (which 404s).

## Impact

- Code: `pkg/cache/cache.go` (`coordinateDownload` post-lock check ~6466; NAR download-lock release lifecycle ~6541-6560; CDC chunking completion path). Possibly `inflight_staging*.go` activation conditions.
- Tests: chunking-window e2e drivers re-enabled; new internal tests for the chosen mechanism.
- I/O / latency / memory: no steady-state change to the single-reader fast path. Depending on the chosen mechanism, a per-NAR download lock may be held marginally longer (through chunking) and/or extra staging part-objects may persist briefly during the chunking window — both bounded by existing staging retention/GC. No upstream-bandwidth, network-latency, or memory-footprint regression expected.
- No breaking changes; no API, flag, or schema changes.
