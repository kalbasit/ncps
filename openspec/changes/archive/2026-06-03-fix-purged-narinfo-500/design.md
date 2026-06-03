## Context

`GetNarInfo` (`pkg/cache/cache.go:3302`) resolves a narinfo through three stages:

1. **DB lookup** — `getNarInfoFromDatabase` (line 3330). Its purge guard
   (`cache.go:4264-4327`) purges a narinfo and returns the sentinel
   `errNarInfoPurged` when the narinfo row exists but the NAR is absent from
   storage, no local job is in flight (`hasUpstreamJob`), no remote download is in
   progress, and (for CDC) no in-progress chunk record exists.
2. **Store fallthrough** — on `errNarInfoPurged` the function correctly does *not*
   return (line 3365 skips the sentinel), checks the store (DB-only narinfos miss
   here), and proceeds to an upstream pull.
3. **Upstream pull + re-read** — `prePullNarInfo` (line 3417) fetches and stores
   the narinfo, firing the NAR download in a detached background goroutine. Then
   `getNarInfoFromDatabase` is called **again** at line 3449, and **its result is
   returned verbatim** at line 3460.

The defect is at stage 3. The background NAR download has not completed when line
3449 runs, so the narinfo is served only because the NAR job is tracked
(`hasUpstreamJob` true → purge skipped). When job tracking is missed — observed in
production under Redis distributed-lock contention between the two replicas
(`failed to acquire lock download:nar:… lock already taken`), where
`coordinateDownload` returns via `pollForDownloadOrTakeOver` without registering a
local job — the second `getNarInfoFromDatabase` re-fires the purge guard on the
freshly-pulled narinfo and returns `errNarInfoPurged`. That sentinel reaches the
handler (`pkg/server/server.go:359-378`), which special-cases only
`storage.ErrNotFound` (404) and falls through to `http.Error(err.Error(), 500)` —
producing `HTTP 500: the narinfo was purged`. fsck reports 3013 such backing-less
narinfos, each a permanent 500.

## Goals / Non-Goals

**Goals:**
- `errNarInfoPurged` never reaches the client as HTTP 500.
- A fired purge resolves to 200 (served) when the narinfo path succeeds, or 404
  (Nix upstream fallback) otherwise.
- No unbounded re-pull/re-purge loop.

**Non-Goals:**
- Fixing the NAR job-tracking race under distributed-lock contention (root cause
  of missing NARs) — tracked separately.
- Reconciling the existing orphaned-narinfo/nar_file backlog.
- Any change to `/nar/...` serving (governed by `nar-cache-miss-recovery`).

## Decisions

**D1 — Map `errNarInfoPurged` to 404 in `GetNarInfo`, not 500.** In `GetNarInfo`,
the post-pull `getNarInfoFromDatabase` result (line 3449) is inspected: if it is
`errNarInfoPurged`, return `storage.ErrNotFound` instead of the sentinel. A purge
firing *immediately after a fresh upstream pull* means the cache cannot confirm a
servable asset, so a 404 (letting Nix fall back to its next substituter) is the
correct, terminal outcome for that request — not a 500, and not a retry loop.
*Alternatives considered:* (a) a bounded re-pull retry loop — rejected: adds
latency and can still fail to converge while the NAR race persists; (b) threading
the freshly-pulled in-memory narinfo out of `pullNarInfo` via `downloadState` to
serve 200 — rejected for this change: larger surface (touches `downloadState`),
and the subsequent `/nar` request would 404 anyway under `nar-cache-miss-recovery`,
so 404-now is consistent.

**D2 — Handler defense in depth.** The narinfo `GET` handler maps
`errNarInfoPurged` to 404 (alongside `storage.ErrNotFound`), so even if the
sentinel ever escapes again it cannot become a 500 or leak its message into a
response body. *Alternative:* rely solely on D1 — rejected; a cheap backstop
prevents regressions if another call site returns the sentinel.

**D3 — Leave the stage-2 fallthrough unchanged.** The first-lookup purge handling
(line 3365) already does the right thing (fall through to upstream). Only the
stage-3 return path and the handler change.

**D4 — Only the purge sentinel is converted.** Transient upstream errors
(non-`ErrNotFound`) from the pull continue to surface unchanged, preserving
retryability per `upstream-fetch-resilience`. The conversion is narrowly scoped to
`errors.Is(err, errNarInfoPurged)`.

## Risks / Trade-offs

- **A racy purge of a genuinely-cacheable narinfo now yields 404 instead of an
  eventual 200.** → Mitigation: 404 triggers Nix's substituter fallback (correct,
  non-fatal), and the next request re-pulls; the underlying NAR-race fix (separate
  change) restores the 200 fast-path. Strictly better than today's permanent 500.
- **Masking a real defect behind a 404.** → Mitigation: the purge already logs at
  Error level (`cache.go:4318`); observability is unchanged. Only the client-facing
  status changes.
- **Sentinel-matching drift.** → Mitigation: both call sites use
  `errors.Is(..., errNarInfoPurged)`; the sentinel stays unexported and centralized.

## Migration Plan

Pure Go code change in `pkg/cache/cache.go` and `pkg/server/server.go`. No schema,
migration, config, or API-shape changes. Forward-compatible and backward-compatible
(it only removes an erroneous 500). Deploy via the normal rollout; rollback is a
plain revert with no state implications. TDD: add failing cache + handler tests
asserting 404/200 (never 500) before implementing.

## Testing Notes

The client-facing 500 is only reachable via a background NAR-download timing
race (the NAR job is gone, NAR absent, narinfo freshly in the DB), which cannot
be reproduced deterministically through the public `GetNarInfo` API. To make the
bug deterministically testable, `pullNarInfo` honours an unexported,
context-scoped test seam (`withNarPrefetchDisabled`) that skips the background
NAR prefetch — mirroring the existing test-influencing patterns in this package
(`WithUploadOnly`, `SetRecordAgeIgnoreTouch`). It has no effect in production
(the flag is never set on real request contexts).

## Open Questions

- None blocking. Whether to later thread the freshly-pulled narinfo through
  `downloadState` to serve 200 in the racy case is deferred to the NAR-race fix.
