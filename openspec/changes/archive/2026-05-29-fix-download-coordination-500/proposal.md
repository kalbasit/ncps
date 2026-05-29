## Why

When ncps runs as multiple replicas sharing a Redis lock backend, a client fetching a NAR (or narinfo) that another replica is currently downloading returns **HTTP 500** instead of being served or cleanly missed. Nix treats 500 as a hard error and retries it up to 5×, amplifying load during exactly the concurrent-pull bursts the coalescing logic was meant to smooth. This is a production-impacting correctness bug observed against a 2-pod deployment.

## What Changes

- Fix `coordinateDownload` (`pkg/cache/cache.go`) so a replica that **loses the distributed lock race no longer returns 500** when its storage-poll times out.
- Make the lock-losing waiter **wait for the holder's terminal state (success *or* failure), not just success**: today the poll loop (`cache.go:5442`) only watches `hasAsset`, so it cannot learn the holder failed and dead-ends in a 500.
- When the holder finishes or releases the lock without producing the asset, the waiter **re-attempts lock acquisition and takes over the download** (serialize, don't parallelize). This keeps coordination correctness-preserving — exactly one downloader at a time — and avoids fanning out concurrent same-hash downloads into the unsafe concurrent-CDC path (#1289).
- On genuine give-up (the asset is absent and cannot be produced), return a **clean cache miss (404), never 500**, so Nix falls back to the next substituter instead of retrying 5×.
- Revisit the poll-timeout vs. lock-TTL relationship so a legitimately slow large-NAR download (holder refreshing its TTL) does not trip waiters into a give-up.
- Add a distributed regression test that reproduces the 500 by holding the lock past the poll timeout while concurrent requests arrive, asserting they succeed (or 404) instead of erroring.

## Capabilities

### New Capabilities
<!-- None — this is a correctness fix to existing coalescing behavior. -->

### Modified Capabilities
- `nar-concurrent-streaming`: a replica that fails to acquire the download lock and times out polling MUST fall back to fetching/serving rather than returning 500.
- `narinfo-concurrent-fetch`: the same lock-loss fallback applies to narinfo coordination (`waitForStorage=true`).

## Impact

- **Code**: `pkg/cache/cache.go` (`coordinateDownload`, the poll-timeout branch and its `downloadError` mapping); `pkg/cache/cache_distributed_test.go` (new regression scenario). Possibly `pkg/server` only if the error-to-status mapping needs adjustment.
- **APIs / behavior**: HTTP responses under contention change from 500 → success or 404. No config schema or wire-format change.
- **Dependencies**: none added.

### Impact on I/O / network / memory
- **Network**: replaces a 500-driven 5× Nix retry storm with either a served response or a single clean 404. Because the waiter takes over only *after* the holder releases the lock (rather than fetching in parallel), it does not add a concurrent duplicate upstream fetch; net upstream traffic should drop under bursty concurrent pulls.
- **I/O**: serialized take-over means at most one writer per hash at a time, so no new concurrent-write contention class is introduced (and the unsafe concurrent-CDC path is avoided).
- **Memory**: negligible — no new buffering or pooling; existing zstd/stream pooling is unchanged.

## Non-goals

- Not changing the lock backend, TTL/retry config defaults, or the Redis locking algorithm.
- **Not** introducing parallel/concurrent same-hash downloads as the fallback — that would exercise the unsafe concurrent-CDC chunking path tracked in #1289 and the `cache_distributed_test.go:645` TODO.
- Not redesigning CDC serve-during-chunking behavior (deferred to #1289).
- Not altering the legitimate 404 ("does not exist in binary cache") path for genuinely-absent upstream paths.
- Not adding new config tunables; any poll-timeout adjustment stays within existing semantics.
