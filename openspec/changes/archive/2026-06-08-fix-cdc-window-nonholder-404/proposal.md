## Why

The in-flight NAR staging contention e2e (`task test:inflight-staging-contention`) is green in the download window across local and s3, but **both chunking-window phases fail**: under CDC, concurrent readers on the **non-downloading replica receive HTTP 404** for the NAR while readers on the holder replica get 200 (a clean 4/8 split by replica, no producer error, no staging serve). This directly violates the existing `inflight-nar-staging` requirement *"Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes"* — the #1289 chunking-window case staging is meant to cover. It is a distinct defect from the staging-producer temp-file race (already fixed): that fix made the download window work, but the chunking window 404s before staging can help.

Investigation split the defect into two layers. This change ships **Layer 1** (the coordination routing); **Layer 2** (the CDC holder produces no staging parts) is a separate follow-up — without it the user-visible 404 persists, so the chunking-window e2e green moves to the Layer-2 change.

## What Changes

- **Stop the non-holder mis-routing an actively-chunking NAR to a chunk-serve 404.** In the download-coordination poll loop (`pollForDownloadOrTakeOver`), the lock-loser early-returned a "served-by-peer" state for an *actively-chunking* NAR because `isServable` conflates "finished" with "actively chunking". Split a finished-only predicate (`hasFinishedNar`: whole-file in store OR `total_chunks>0`) out of `isServable`, and restructure the per-tick decision into four states: dead-holder→takeover (D5, first); finished→serve; actively-chunking + staging-ready→serve from staging (transcoding); actively-chunking + no-staging→progressive chunk streaming (unchanged). The net effect: when staging parts exist, a contended CDC loser serves them (no 404); the no-staging progressive path is preserved byte-for-byte.
- **Layer 2 (separate change):** wire the in-flight staging *producer* into the CDC download path so the holder actually produces parts during chunking. Today it does not, so `stagingServeReady` is always nil at the loser and the 404 persists end-to-end. That change owns the chunking-window e2e green.

## Capabilities

### Modified Capabilities
- `inflight-nar-staging`: strengthen the "Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes" requirement with a scenario asserting that, *when staging parts are available*, a lock-losing reader under CDC serves from staging rather than routing to chunk serving and 404-ing a compressed request.

## Impact

- **Code**: `pkg/cache/cache.go` — `hasFinishedNar` helper; `hasFinishedAsset` threaded through `coordinateDownload` + `pollForDownloadOrTakeOver`; the poll-tick state split. TDD: `pkg/cache` unit test (`hasFinishedNar` excludes actively-chunking) + full `-race` suite (no regression to progressive-streaming/takeover/CDC/recovery).
- **I/O / latency / memory**: no steady-state change; only the contended-loser routing decision changes.

## Non-goals

- **Layer 2** — wiring the CDC staging producer to emit parts (separate change; owns the chunking-window e2e green).
- The download-window staging fix (already shipped in `fix-inflight-staging-producer-temp-race`).
- Reworking the CDC chunking algorithm, part-object format, or drain/migration paths.
- The e2e harness itself.
