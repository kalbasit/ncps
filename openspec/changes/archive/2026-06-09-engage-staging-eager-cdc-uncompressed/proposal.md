## Why

In-flight NAR staging is documented (CHANGELOG; #1355/#1374/#1375/#1379) as serving cross-pod reads during the eager-CDC chunking window — *"preferring staging over the fragile progressive-chunk reassembly."* But it never actually engages for the **uncompressed `.nar`** request, which — since predictive-`none` (#1380) — is the **only** request clients make in that window. A new e2e proves it: the cross-pod reader is served by progressive chunks with zero contention and zero staging. The gap is two-site:

- `cache.go:1320` routes an actively-chunking read to download coordination (the path that records a staging request and serves from staging) **only when `Compression != none`** — so the uncompressed read short-circuits to progressive chunk serving and never contends.
- Poll-loop state (D) at `cache.go:6815` returns "served-by-peer → progressive chunks" as soon as the NAR is actively-chunking with no staging parts *yet*, pre-empting staging before the holder's producer can stage.

`#1379` itself flagged this path as unit-verified but with "no green end-to-end run yet." This change closes the gap so the documented behavior is real.

## What Changes

- **Route uncompressed actively-chunking cross-pod reads to coordination when staging is enabled** (`cache.go:1320`): extend the not-servable fall-through to `Compression == none` under `InflightStagingEnabled()`, so the reader contends, records a staging request, and serves from in-flight staging instead of progressive chunks.
- **Let poll-loop state (D) wait briefly for staging parts** (`cache.go:6815`): when staging is enabled and a staging request was recorded, keep polling (bounded) for staging parts so (C) "serve from staging" can win, before falling back to progressive chunks as the safety net.
- Both behind `InflightStagingEnabled()` (default off); staging-disabled, lazy-CDC, single-replica/same-pod, and finished-NAR paths are unchanged.
- Flip the `staging-contention` e2e chunking-window assertion to require **staging activation** (the design intent), reverting the interim progressive-chunk assertion.
- Correct the CHANGELOG to reflect that this is the change that actually makes chunking-window staging engage.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `inflight-nar-staging`: clarify that an **uncompressed** cross-pod read during the eager-CDC chunking window engages in-flight staging (contends, records a request, serves from staging), preferring staging over progressive chunks; progressive chunks remains the bounded fallback only when staging parts never materialize.
- `unified-e2e-harness`: the `staging-contention` chunking window asserts staging activation again (not progressive-chunk serving).

## Impact

- **Code**: `pkg/cache/cache.go` (GetNar routing at ~1320; `pollForDownloadOrTakeOver` state D at ~6815). Hot serve path — concurrency-critical.
- **Tests**: new `pkg/cache` internal unit tests (red→green) for both sites; `nix/e2e-tests/src/phases/staging_contention.py` chunking-window assertion flipped to staging activation.
- **I/O / latency / memory**: when staging engages, a contending cross-pod reader tails staging part-objects (existing mechanism) instead of progressive chunks — comparable I/O, more robust (no truncation). Bounded extra poll latency (≤ a few 200 ms ticks) before the staging fallback. No change when staging is disabled.
- **Non-goals**: not changing the staging producer, part-object format, GC, or holder-death recovery; not touching compressed-request handling (already coordinates), lazy CDC, or the download window; not altering predictive-`none`.
