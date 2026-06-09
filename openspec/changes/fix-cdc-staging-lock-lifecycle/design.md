## Context

The in-flight staging feature (change `serve-whole-nar-in-flight`, merged) lets a cross-pod reader serve a NAR that a peer is still downloading, by tailing staging part-objects the holder commits to shared storage. A follow-up stack (#1374→#1375→#1376) made this work for the **download/decompress window** and split `narServability` into `(servable, finished)` so the coordination poll loop stops chunk-404'ing a compressed request for an in-flight NAR. That stack is assumed merged; this change builds on it.

What remains broken is the **CDC chunking window**. Two distinct per-hash locks bracket two distinct phases:

- `download:nar:<hash>` (`narJobKey`) — held while the NAR **bytes** are fetched/decompressed. Released as soon as `ds.stored` closes (`coordinateDownload`, `waitForStorage=false`, cache.go:6541-6560).
- `migration:<hash>` (`migrationLockKey`) — held by the chunker for the **whole chunking operation** (cache.go:9367). Its liveness is already the signal `narServability` uses to call an actively-chunking row `servable && !finished` (cache.go:4499-4516, via `cdcChunkerLive`).

The chunking window is bracketed by the **migration** lock, *not* the download lock. After `ds.stored`, the download lock is **free** while chunking runs in the background. So a cross-pod reader arriving mid-chunking *acquires* the free download lock (cache.go:6427) instead of contending — it never reaches `pollForDownloadOrTakeOver`, never calls `markStagingRequested`, and hits the post-lock check at **cache.go:6466**, which uses `servable` (the first `checkAsset` return), not `finished`:

```go
if servable, _ := checkAsset(ctx); servable {   // actively-chunking row ⇒ true
    ... "asset already in storage, skipping download"   // → completed ds (no stagingServe)
}
```

GetNar then serves that completed `ds` via `serveNarFromStorageViaPipe` with `hasNarInStore=false` → `serveFromChunks=true` → and for a compressed (`.nar.xz`) request it returns `ErrNotFound` (cache.go:3481-3484), because chunks are stored decompressed.

**Scope nuance — only the eager path 404s.** The *lazy* path stores the whole NAR via `storeNarFromTempFile` and *then* background-migrates to chunks (cache.go:3386-3400), so during chunking `HasNarInStore==true` and the compressed request serves from the whole file. The 404 is specific to the **eager** path (`pullNarIntoStore`, `keepJobAlive=true` at 3211/3332), which chunks progressively during download and never stores a whole file — so mid-chunking the only complete representation is the holder's **node-local temp file**. On S3 there is no API to read another pod's in-progress bytes; a cross-pod reader's only complete source is the holder staging them.

## Goals / Non-Goals

**Goals:**
- A compressed request for an actively-chunking NAR is served (from staging) or cleanly coordinated to wait — never chunk-404'd, never forced to upstream fallback.
- Staging activation and the holder's producer track the **chunking** window (migration lock), not just the download-lock window.
- Fix the `coordinateDownload` post-lock check (cache.go:6466) without triggering a redundant re-download or a double-chunk that conflicts with the holder's migration lock.
- Re-enable the chunking-window e2e as proof-of-fix.

**Non-Goals:**
- The download-window path (already green via #1374–#1376).
- Re-architecting the staging substrate (`staging_state`, part-objects, GC, takeover) — reused as-is.
- New config flags, DB schema, or migrations. Behavior-only, behind the existing (default-off) inflight-staging flag.
- Non-CDC / non-HA behavior; lazy-vs-eager policy; the chunk format; serving compressed variants from a *fully* chunked NAR (a separate concern).

## Decisions

> **Revised after reading the code (see Decision Log).** The first draft chose a finished-aware fix at the `coordinateDownload` post-lock check (cache.go:6466). Reading the eager-CDC download/chunking/producer lifecycle showed the chosen mechanism below (Option A) is both smaller and lower-risk, and that the producer/temp-file liveness the patch would have added already exists.

### D1 — Hold the NAR download lock through the eager-CDC chunking window
For a NAR (`waitForStorage == false`), `coordinateDownload` releases the `download:nar:<hash>` lock in a background goroutine that today waits on `ds.stored` **or** `ds.done` (cache.go:~6541-6560). `ds.stored` closes at chunk *start* (`onNarFileReady`, cache.go:3249-3251), so the lock frees while chunking runs. Change that release to wait on **`ds.done` only**. For eager CDC, `ds.done` closes only after chunking completes (the CDC goroutine's defer, cache.go:3227, runs after `storeNarWithCDC*`); for non-CDC and lazy CDC, `ds.done` closes essentially together with `ds.stored`, so their lock timing is unchanged. The lock TTL refresher already runs until release, so holder death during chunking is still recovered by TTL expiry + takeover.

With the lock held through chunking, a cross-pod reader arriving mid-chunking **contends** (its `coordinateDownload` lock acquisition fails) and falls into `pollForDownloadOrTakeOver` — exactly the download-window contention path already shipped and tested in #1374/#1375. There it records a staging request (`markStagingRequested`) and serves from staging (compressed) or progressive chunks (uncompressed). The acquire-a-free-lock path at cache.go:6466 is therefore never reached with an actively-chunking row, so that site needs no change.

*Alternatives considered:*
- *Fix the post-lock check at 6466 (original D2):* rejected. `coordinateDownload` is compression-agnostic, and a reader that has **acquired** the lock cannot simply delegate to `pollForDownloadOrTakeOver` — that re-attempts `TryLock`, succeeds on the now-free lock, takes over, and re-downloads + re-chunks against the holder's live migration lock (double-chunk hazard). Avoiding that needs a bespoke takeover-suppressing wait loop plus threading the requested compression into coordination — materially more code and risk than Option A.
- *Key activation on the migration lock instead of extending the download lock:* rejected as unnecessary — it duplicates a contention signal Option A already gets for free, and requires new wiring.

### D2 — Producer liveness and temp-file availability: verified, no new code
The `stageInflightNar` producer polls `staging_state` until `ds.done` (`waitForStagingRequest`, inflight_staging.go:157-178), and `ds.assetPath` (the chunker's decompressed input) is removed only by the post-chunking cleanup goroutine (it waits on `ds.cdcWg` before `os.Remove`, cache.go:3146-3162). So for eager CDC the producer is **already alive** through the chunking window and the temp file is **already present** — a staging request recorded mid-chunking (now produced by the contending reader under D1) is observed and staged from temp with no new machinery. Implementation confirms this with a test rather than adding code.

### D3 — Existing death/takeover semantics are sufficient
Holder death during chunking is already handled: the download-lock TTL expires (refresher stops with the process), a peer takes over via `pollForDownloadOrTakeOver`, and `resetStagingForTakeover` + the GC sweep reclaim truncated staging parts. `narServability` already reports a free-migration-lock chunking row as not-servable (#1230), preserving clean re-download. No new liveness machinery.

## Risks / Trade-offs

- **Producer goroutine + temp-file retained through chunking** → bounded by the migration-lock hold; the GC sweep reclaims orphaned staging artifacts; producer no-ops on ENOENT (existing temp-race fix). Mitigation: tie producer lifetime to the migration lock / chunking completion signal, not an open-ended timer.
- **Staging part-objects persist slightly longer (into the chunking window)** → extra S3 objects bounded by existing `--cache-inflight-staging-retention` + GC. No steady-state change to the single-reader fast path.
- **Very large NARs: chunking outlasting lock TTL** → migration lock already runs a refresher and `narServability` treats a held lock as live regardless of age; no new exposure.
- **Test flakiness — the chunking window is brief** → e2e must force slow chunking to observe the window deterministically; reuse the existing contention/lifecycle drivers' knobs.
- **Interaction with #1374–#1376 if they land in a different shape** → this change depends on `narServability`/`hasFinishedNar` and `markStagingRequested`/`stagingServeReady`; verify those symbols exist before implementing.

## Migration Plan

- Behavior-only: no schema, migration, flag, or API change. Ships behind the existing default-off inflight-staging flag, so production is unaffected until an operator opts in.
- Depends on the #1374–#1376 stack being merged first.
- Rollback = revert the commit; the staging substrate and the download-window path are untouched.

## Resolved Questions (from code reading)

1. **Does the eager temp file persist for the entire chunking window?** Yes. `ds.assetPath` (the decompressed bytes the chunker reads via `fileAvailableReader`) is removed only by the cleanup goroutine, which waits on `ds.cdcWg` — i.e. after chunking — before `os.Remove` (cache.go:3146-3162). The producer can stage from it throughout chunking.
2. **Is the lazy path immune?** Yes. The lazy path stores the whole NAR (`storeNarFromTempFile`) and *then* background-migrates to chunks (cache.go:3386-3400), so during chunking `HasNarInStore == true` and a compressed request serves from the whole file. The 404 is eager-only. The brief `migrateNarToChunksCleanup` whole-file-delete window is already covered by the store→chunks TOCTOU fallback (cache.go:3491-3499); a regression test guards it.
3. **Dead-orphan recovery preserved?** Yes, and Option A does not touch it: a chunking row whose migration lock is free makes `narServability` return not-servable (#1230), so GetNar re-downloads cleanly. Option A only changes *download-lock* release timing for a *live* eager chunk.

## Decision Log

- **Initial design:** finished-aware fix at `coordinateDownload` post-lock (cache.go:6466) + explicit producer-liveness work (D1/D2/D3 of the first draft).
- **After reading the eager-CDC download/chunking/producer lifecycle:** switched the mechanism to **holding the download lock through the chunking window** (Option A). Reason: the producer and temp file already survive the chunking window (so no liveness code is needed), and the post-lock fix would have required a takeover-suppressing wait loop + compression threading to avoid a double-chunk re-download. Option A collapses the chunking-window case into the already-tested download-window contention path with a one-line release-timing change. Approved by the user.

## Post-implementation finding (the contention e2e)

Running `task test:inflight-staging-contention --window chunking` (xz upstream) showed the lock-fix removed the 404 but served **corrupt** bytes for the `.nar.xz` request: for eager CDC the in-flight temp/staging is **decompressed** (`none`), and ncps has **no NAR compressor** (a re-compressed file would not match the narinfo `FileHash`/`FileSize`), so a compressed variant cannot be reconstructed. **Corrected contract (this change):** an uncompressed (`none`) cross-pod request mid-chunking is served from staging; a **compressed** request returns not-found → upstream fallback (added as **Guard A** in `serveNarFromStaging`). This matches ncps's serve-time narinfo normalization, which only advertises `none` once the NAR is genuinely chunked.

**Deferred (separate session):** the corruption's real root is that the narinfo advertises `xz` during chunking (so clients request `.nar.xz` at all). Making the narinfo advertise `none` for CDC NARs would eliminate the compressed request entirely — but doing so for the cold/triggering client requires predictive normalization, which reopens the bug-prone `nix copy`/upload-desync area (cache.go:4082). The same-pod temp path has the identical pre-existing corruption (a guard there was reverted because it breaks the deliberate `cdc_test.go:676` test). The contention e2e's chunking-window expectation (serve compressed from staging) is itself wrong for eager CDC and needs redesigning. See memory `project_cdc_narinfo_none_root_fix` for the full design + risk analysis.
