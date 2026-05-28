## Context

`pkg/cache/cache.go` has two layers of concurrency control for upstream fetches:

1. **`coordinateDownload`** (line ~5318): takes a `narInfoJobKey` or `narJobKey`, checks `upstreamJobs`, and if no job is in-flight, acquires a distributed lock (`downloadLocker`), re-checks `hasAsset`, then starts a goroutine and registers in `upstreamJobs`. Concurrent callers for the same key piggyback onto the running job via `ds.start`/`ds.stored`/`ds.done` channels.

2. **`getNarInfoFromDatabase`** (line ~4078): called by `GetNarInfo` after the narinfo is stored, to validate it. Contains a purge guard: if `HasNarInStore(narURL)` returns false AND `hasUpstreamJob(narURL.Hash)` returns false AND `isRemoteDownloadInProgress` returns false, it purges the narinfo from DB and returns `errNarInfoPurged`.

The race is in `getNarInfoFromDatabase`'s purge guard. The sequence of checks is non-atomic:

```text
// goroutine A (concurrent request that piggybacked)
1. HasNarInStore(narURL) → false        // NAR rename not yet visible
   [goroutine B's NAR download completes: os.Rename(tmp→narPath)]
   [goroutine B's pullNarInfo deferred done() removes job from upstreamJobs, closes ds.done]
2. hasUpstreamJob(narURL.Hash) → false  // job just removed
3. isRemoteDownloadInProgress  → false  // lock released
4. PURGE triggered: narinfo deleted from DB
```

`pullNarInfo` calls `prePullNar` (starts NAR download goroutine) BEFORE `storeInDatabase`, so the narinfo lands in DB while the NAR download may still be in flight. When `ds.done` fires, piggybacking goroutines call `getNarInfoFromDatabase` and can hit the TOCTOU window above, triggering a spurious purge.

A subsequent concurrent call then finds narinfo absent from DB, returns `storage.ErrNotFound`, and the HTTP handler returns 404.

## Goals / Non-Goals

**Goals:**
- Eliminate the TOCTOU race in `getNarInfoFromDatabase`'s purge guard so concurrent narinfo fetches for the same hash never produce spurious 404s.
- Maintain the existing `coordinateDownload` guarantees for both narinfo and NAR jobs.
- Keep the fix confined to the cache layer; no changes to HTTP handlers or test structure.

**Non-Goals:**
- Rewriting the broader `coordinateDownload` / `upstreamJobs` machinery.
- Adding per-request timeouts or retry logic to `GetNarInfo`.
- Fixing any other race conditions beyond the narinfo purge TOCTOU.

## Decisions

**Fix the purge guard race by extending the job window through NAR download completion.**

`pullNarInfo` currently fires `ds.done` (via the deferred `done()`) immediately after narinfo is stored in DB — before the NAR download goroutine spawned by `prePullNar` finishes. The purge guard in `getNarInfoFromDatabase` then has a window where neither `hasUpstreamJob` nor `hasNarInStore` is true.

The fix: the `narInfoJobKey` job must not be removed from `upstreamJobs` until the NAR download has also completed (i.e., until the NAR is visible in the store). Concretely, the `done()` deferred in `pullNarInfo` should wait for the NAR-job channel (`narJobKey`) to complete before closing `ds.done`.

Alternatively, extend `getNarInfoFromDatabase`'s purge check to also check whether a NAR job for the same hash is in-flight:

```go
// In getNarInfoFromDatabase, before purging:
if hasUpstreamJob(narURL.Hash) || hasNarUpstreamJob(narURL.Hash) || isRemoteDownloadInProgress {
    // don't purge
}
```

This second approach is simpler and less invasive: it adds a check for the NAR job key (`"download:nar:" + hash`) alongside the existing narinfo job key check. If the NAR download is still running (job registered in `upstreamJobs`), the purge is skipped — the purge guard will be re-evaluated on the next read (the caller retries via the existing retry path).

**Chosen approach: extend `getNarInfoFromDatabase` purge guard to check NAR job in-flight.**

This is the minimal, targeted change:
- One new helper or extend `hasUpstreamJob` to check both `narInfoJobKey` and `narJobKey`.
- No changes to `pullNarInfo`, `coordinateDownload`, or channel signaling.
- The fix is self-contained within the purge guard block.

`hasUpstreamJob(hash)` currently checks `narJobKey(hash)` only. We will also check `narInfoJobKey(hash)` — or more precisely, `hasUpstreamJob` should be extended (or a new `hasAnyUpstreamJob(hash)` added) that returns true if EITHER key is registered.

## Risks / Trade-offs

- **Missed window shrinks but doesn't disappear**: the NAR job exists from when `coordinateDownload` registers it until its `done()` deferred fires. If the NAR download finishes and the job is removed between `HasNarInStore` and `hasAnyUpstreamJob`, the same TOCTOU window exists. However, `os.Rename` atomicity ensures that once `HasNarInStore` returns false, the rename hasn't happened, which means the NAR goroutine hasn't reached its final step. The job is still in `upstreamJobs` at this point. So checking both keys closes the gap.

- **`isRemoteDownloadInProgress` still covers distributed scenarios**: the distributed lock path (`TryLock`) already handles the multi-instance case. The fix only tightens the single-instance race.

- **Retry behavior**: when purge is skipped, `getNarInfoFromDatabase` still needs to return something useful to `GetNarInfo`. The existing error path (`errNarInfoPurged`) causes `GetNarInfo` to retry via `prePullNarInfo`. Since we're not purging, we can return the narinfo row directly (no change needed; purge is simply not triggered).
