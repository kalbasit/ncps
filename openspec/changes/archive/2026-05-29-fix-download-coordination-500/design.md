## Context

In a multi-replica deployment with a Redis lock backend, ncps coalesces duplicate
downloads of the same NAR/narinfo hash through `coordinateDownload`
(`pkg/cache/cache.go:5386`). Coordination has two layers:

- **Intra-pod**: an in-process `upstreamJobs` map keyed by lock key. A second
  local goroutine waits on the shared `downloadState` channels (`ds.start`,
  `ds.stored`, `ds.done`) and can observe the holder's error via
  `ds.getError()`.
- **Cross-pod**: a Redis lock (`download:nar:<hash>` / narinfo equivalent).
  Replicas do **not** share `upstreamJobs`, so a replica that fails to acquire
  the lock has no `downloadState` to observe — it only knows "someone else holds
  the lock."

The bug lives in the cross-pod branch. When `c.downloadLocker.Lock` fails
(`cache.go:5422`), the waiter enters a poll loop (`cache.go:5442`) that **only**
checks `hasAsset` every 200ms until `downloadPollTimeout` (default 30s) expires.
On timeout it sets a generic `downloadError`
("failed to acquire download lock and timeout polling for completion", `cache.go:5469`),
which the server maps to **HTTP 500** (non-`ErrNotFound`, non-context error).

Two real-world triggers, both observed in production logs:

1. The lock holder's download **fails** (upstream HTTP/2 reset → "truncated
   input", or upstream 404). The asset will never appear, so every waiter burns
   30s then 500s.
2. The lock holder is **legitimately slow** (large NAR), holding and refreshing
   its 5-minute lock TTL well past the 30s poll window. Honest slow downloads
   also 500.

A 500 is the worst outcome: Nix treats it as a hard error and retries up to 5×,
amplifying load during exactly the concurrent-pull bursts coordination exists to
smooth.

## Goals / Non-Goals

**Goals:**
- A lock-losing waiter NEVER returns HTTP 500 due to coordination.
- When the holder succeeds, waiters serve the now-present asset.
- When the holder fails/releases without producing the asset, a waiter
  **re-acquires the lock and takes over** the download (serialized, one
  downloader at a time).
- When the asset is genuinely absent (upstream 404 even after take-over), return
  a clean **cache miss (404)** so Nix falls back gracefully.
- Keep exactly-one-downloader semantics so the unsafe concurrent-CDC path
  (#1289, `cache_distributed_test.go:645`) is never exercised by this fix.

**Non-Goals:**
- No parallel/concurrent same-hash downloads as a fallback.
- No change to the lock backend, retry config, or Redis algorithm.
- No redesign of CDC serve-during-chunking (tracked separately in #1289).
- No change to the legitimate "does not exist in binary cache" 404 path.
- No new user-facing config tunables.

## Decisions

### Decision 1: Replace poll-only-then-500 with poll-or-reacquire-then-takeover

The cross-pod fallback (`cache.go:5442-5477`) changes from a dead-end poll loop
into a bounded loop that, on each tick, does two things:

1. `hasAsset(ctx)` → if present, the holder succeeded; return a completed
   `downloadState` and serve (unchanged success path).
2. Re-attempt `c.downloadLocker.Lock(...)` → if it now succeeds, the previous
   holder has released (finished or failed) and the asset is still absent, so
   **this replica becomes the holder** and proceeds into the existing
   post-lock download path (`startJob`). This is the take-over.

Because the holder refreshes its TTL while actively working
(`lock.StartRefresher`, `cache.go:5481`), re-acquisition only succeeds once the
holder is truly done — so take-over naturally serializes and never overlaps a
live download.

**Why over alternatives:**
- *Waiter fetches in parallel after timeout* (the original proposal wording):
  rejected — two pods writing the same hash is safe for storage/DB (idempotent
  upserts, atomic rename) but **not** for CDC lazy chunking, which has a known
  concurrent-reconstruction defect (#1289). Take-over keeps a single writer.
- *Just return 404 immediately on lock-loss*: rejected — gives up on serving an
  asset the cache can produce; wastes the coalescing benefit.

### Decision 2: Terminal give-up maps to a cache miss (404), never 500

If the loop's overall deadline is reached without the asset appearing and
without successful take-over (e.g., the holder is still legitimately mid-download
and refreshing its TTL), the coordination path returns a result the server maps
to **404**, not 500. Concretely, the give-up returns `storage.ErrNotFound`
(server: `ErrNotFound → 404`) rather than a generic error.

**Why:** for Nix, 404 = "not in this cache" → fall back to the next substituter
or build locally; 500 = retry storm. 404 is the correct degraded signal.

### Decision 3: Align the give-up deadline with the holder's lock TTL

The 30s `downloadPollTimeout` being shorter than a legitimate large-NAR download
is itself a 500 source. The waiter should be willing to wait up to roughly the
holder's lock TTL (the window in which the holder could still be making
progress) before giving up. We bound waiting by the lock TTL (or
`min(coordCtx deadline, downloadLockTTL)`) rather than a fixed 30s, while still
preferring take-over the moment the lock frees.

**Why over alternatives:** raising the fixed poll timeout alone would still 500
on holder failure; tying the bound to the TTL plus the take-over loop covers
both the slow-holder and failed-holder cases.

### Decision 4: Scope identically to NAR and narinfo coordination

Both `waitForStorage=false` (NAR streaming) and `waitForStorage=true` (narinfo)
flow through `coordinateDownload`, so the fix is applied once at the coordination
layer and covered by specs for both `nar-concurrent-streaming` and
`narinfo-concurrent-fetch`.

## Risks / Trade-offs

- **[Take-over after a failing upstream re-tries the same failing fetch]** →
  Mitigation: the take-over runs the normal `startJob`, which returns
  `ErrNotFound` for a genuine 404; that maps to a clean 404, not an infinite
  loop. Transient upstream errors surface as the download's own error, unchanged
  from single-pod behavior.
- **[Waiting up to the lock TTL increases tail latency for the failed-holder
  case]** → Mitigation: take-over fires as soon as the lock frees (holder
  failure releases promptly), so the long wait only applies when the holder is
  genuinely still progressing — exactly when waiting is correct.
- **[Re-acquisition adds Redis Lock calls during contention]** → Mitigation:
  re-attempt on the existing 200ms poll cadence (not a tight spin); negligible
  additional Redis load versus the eliminated 5× Nix retry storm.
- **[Thundering herd of take-over on a popular failed hash]** → Mitigation:
  serialization still holds — only the single replica that wins re-acquisition
  downloads; others keep polling/serving from its result.

## Migration Plan

- Pure behavioral fix inside `coordinateDownload` and its error mapping; no
  schema, config, or wire-format change.
- Deploy is a rolling restart. Mixed-version replicas during rollout remain
  safe: old replicas still 500 on lock-loss (pre-fix behavior), new replicas
  take over / 404; correctness of stored data is unaffected because storage/DB
  writes are already idempotent.
- Rollback is a redeploy of the previous image; no state to revert.

## Open Questions

- Should we add a lightweight "is this lock still held?" introspection to the
  locker interface to distinguish "holder progressing" from "holder gone"
  deterministically, instead of inferring it from a failed re-acquire? (Cleaner,
  but expands the `Locker` interface — deferred; re-acquire inference proved
  reliable in the regression tests.)

## Resolved During Implementation

- **Give-up deadline value**: `giveUpBound = max(downloadLockTTL,
  downloadPollTimeout)`. The lock TTL is the primary bound (the window a live
  holder could still be refreshing its lock and making progress); the legacy
  `downloadPollTimeout` is kept as a lower bound so existing configuration still
  has effect. Take-over fires as soon as the lock frees, so the full bound is
  only reached when a holder is genuinely still progressing.
- **Observability**: structured logs — `Warn` on give-up ("gave up waiting for
  download by another server, returning cache miss") and `Debug` on take-over
  ("re-acquired download lock, taking over the download") — plus a Prometheus
  counter `ncps_download_coordination_fallback_total` with an `outcome`
  attribute (`served_by_peer`, `take_over`, `give_up`, `caller_canceled`), so
  operators can see how lock contention resolves. (Note: the counter binds the
  global OTel meter at package `init()`, matching every other counter in this
  package; like them, it is not unit-tested.)
- **Implementation shape**: the fallback was extracted into
  `(*Cache).pollForDownloadOrTakeOver`, returning `(ds, tookOver)` so
  `coordinateDownload` stays flat (resolves a `nestif` lint finding) and the
  take-over path simply falls through to the existing post-lock code.
- **Tests**: covered by `pkg/cache/coordination_internal_test.go` using a
  `takeoverLocker` mock (no Redis required, deterministic). The Redis-backed
  distributed suite (`task test:auto` with `enable-redis-tests`) remains the
  integration check in CI's per-backend derivations.
