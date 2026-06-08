## Why

Under eager CDC, concurrent readers on the **non-downloading replica receive HTTP 404** for `/nar/<hash>.nar.xz` while the holder serves 200 (the chunking-window contention e2e, 4/8 split). Log evidence pins the cause: the loser has **zero** download-coordination lines — it never reaches `coordinateDownload`/`pollForDownloadOrTakeOver`. It short-circuits in `Cache.GetNar` (cache.go ~1216-1245): `isServable` returns true because the holder's *actively-chunking* `nar_file` row (`total_chunks==0`, `ChunkingStartedAt` set, migration lock held → `cdcChunkerLive`) is visible cross-pod, so the `if hasNar` branch serves from chunks via `serveNarFromStorageViaPipe`, which **404s at cache.go:3465** because chunks are stored decompressed and cannot produce the requested compressed (`.nar.xz`) variant. This violates the `inflight-nar-staging` requirement *"Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes"* (#1289).

The prior change `fix-cdc-window-nonholder-404` fixed the *coordination loop* (`hasFinishedNar` vs `isServable`), but the loser never reaches that loop — so this `GetNar` short-circuit is the missing prerequisite.

## What Changes

- **In `GetNar`, do not serve an actively-chunking NAR from chunks for a compressed request.** When `isServable` is true but the NAR is not finished (`!hasFinishedNar`: not whole-file-in-store and not fully chunked) and the requested compression is not `none`, treat it as not-servable-now so the request falls through to `prePullNar` → coordination. There the loser records a staging request and serves from in-flight staging (which transcodes to the requested compression), exactly as the download window already does. The uncompressed-request progressive-chunk path is unchanged (it can serve from chunks), and finished NARs are unaffected. Reuses the `hasFinishedNar` predicate added by the prior change.
- **Prove it with the e2e.** The chunking-window phases (`--window chunking`, local + s3) of `task test:inflight-staging-contention` must pass: the loser reaches coordination, staging activates, every reader gets a complete byte-identical NAR, no 404.

Downstream unknown (explicitly gated): once the loser records a staging request, the holder's CDC producer must actually emit parts. If the e2e shows the loser now coordinates but staging still does not serve (producer yields nothing), that is a further, separate defect — this change will STOP and report rather than mask it.

## Capabilities

### Modified Capabilities
- `inflight-nar-staging`: a cross-pod reader of an actively-chunking NAR with a compressed request SHALL coordinate and serve from staging rather than being served (404) from chunks that cannot produce the requested compression.

## Impact

- **Code**: `pkg/cache/cache.go` (`GetNar` servability/short-circuit routing). TDD: `pkg/cache` unit test + full `-race` suite (no regression to progressive-chunk or holder paths) + the chunking-window e2e.
- **I/O / latency / memory**: no steady-state change; only the contended cross-pod compressed-request-during-chunking decision changes (it coordinates instead of 404-ing).

## Non-goals

- The coordination-loop split (shipped in `fix-cdc-window-nonholder-404`).
- The steady-state fully-chunked-NAR compressed-request path (narinfo normalizes CDC URLs to `none` post-chunking, so clients request `.nar`); only the transient active-chunking window is in scope.
- Reworking the CDC chunking algorithm, part-object format, or drain/migration paths.
