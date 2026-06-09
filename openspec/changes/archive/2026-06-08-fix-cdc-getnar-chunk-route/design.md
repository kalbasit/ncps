## Context

`Cache.GetNar` (cache.go ~1200-1245) decides whether to serve immediately or coordinate a download:

```go
hasNar, _ := c.isServable(ctx, narURL)            // true for in-store, fully-chunked, OR actively-chunking-live
if hasActiveLocalJob && !hasNarInStore {           // holder path: prefer temp-file streaming
    hasNar, _ = c.HasNarInChunks(ctx, narURL)
}
if hasNar { serveNarFromStorageViaPipe(...) ; return }   // <-- loser short-circuits here under CDC
... prePullNar / coordination ...
```

On the non-holder replica, `hasActiveLocalJob` is false, so `hasNar` stays `isServable == true` (the holder's actively-chunking row is visible via the shared DB). The `if hasNar` branch calls `serveNarFromStorageViaPipe(hasNarInStore=false)`, which sets `serveFromChunks = true` and then returns `storage.ErrNotFound` at cache.go:3465 for a compressed request ("only available as chunks, cannot serve as xz"). The loser never reaches `prePullNar`, so it never records a staging request and never serves from staging. Confirmed by logs: the loser has zero coordination lines.

## Goals / Non-Goals

**Goals:**
- A cross-pod reader of an actively-chunking NAR with a compressed request coordinates (and serves from staging, transcoding) instead of 404-ing from chunks.
- Preserve: the uncompressed-request progressive-chunk path (serve from chunks as bytes land), the holder's own temp-file streaming, and finished-NAR serving.
- Reuse `hasFinishedNar` (added by `fix-cdc-window-nonholder-404`).

**Non-Goals:**
- The coordination-loop split (already shipped).
- The steady-state fully-chunked compressed-request path (narinfo normalizes to `none` post-chunking).
- The downstream "does the CDC producer stage?" question — gated: STOP and report if the e2e shows the loser now coordinates but staging still yields nothing.

## Decisions

**D1 — Gate the `hasNar` short-circuit on (finished OR uncompressed).**
After the existing `hasNar` computation (including the `hasActiveLocalJob` re-eval), add: if `hasNar` is true but `!c.hasFinishedNar(ctx, narURL)` (so it is only servable because it is actively chunking) AND `narURL.Compression != none` (the chunk store, which holds decompressed bytes, cannot satisfy it), set `hasNar = false`. The request then falls through to `prePullNar` → coordination, where the lock-loser records a staging request and serves from staging (transcoding). *Alternatives considered:* (a) make `serveNarFromStorageViaPipe` reassemble+recompress chunks to the requested compression — heavier, duplicates the staging transcode, and still wouldn't record a staging request for the holder to serve other peers; (b) narrow `isServable` — rejected, it is the shared servability oracle used by many call sites and must keep reporting actively-chunking as servable for the uncompressed progressive path.

**D2 — Tight condition; everything else unchanged.**
The new branch fires only for: cross-pod (no local job) + actively-chunking (`isServable && !hasFinishedNar`) + compressed request. Uncompressed requests keep serving from chunks (progressive). Finished NARs (`hasFinishedNar` true) are untouched. The holder (local job) already takes the temp-file path.

## Risks / Trade-offs

- **[Regressing the uncompressed progressive-chunk path]** → The condition requires `Compression != none`; uncompressed requests are unaffected (full `-race` suite, esp. `cdc_*`/progressive tests, must stay green).
- **[A finished fully-chunked NAR with a compressed request still 404s]** → Out of scope (steady-state narinfo advertises `none`); only the transient active-chunking window is fixed. Documented.
- **[The producer may still not stage]** → Explicitly gated: the e2e is the arbiter; STOP+report if staging still yields nothing once the loser coordinates.

## Migration Plan

Pure `pkg/cache` behavior change; no schema/config/API. Rollback is a git revert. The chunking-window e2e is the multi-process gate.

## Open Questions

- Does the CDC holder's staging producer emit parts once a request is recorded? (Answered by the e2e in this change; if no, it becomes a separate follow-up.)
