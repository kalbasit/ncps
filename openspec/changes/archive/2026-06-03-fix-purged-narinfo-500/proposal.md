## Why

A narinfo that exists in the database but whose NAR is missing from storage is
purged by `getNarInfoFromDatabase`, which returns the internal sentinel
`errNarInfoPurged`. That sentinel is not special-cased in the HTTP handler, so it
falls through to `http.Error(w, err.Error(), 500)` — the client receives
`HTTP 500: the narinfo was purged`. Nix treats 500 as a hard error and, after
retries, aborts the build instead of falling back to its next substituter. In
production, fsck currently reports 3013 narinfos without backing NAR files, so a
large set of hashes return a permanent 500 and cannot be fetched at all.

## What Changes

- `errNarInfoPurged` MUST never surface to the client as an HTTP 500. A purge is
  internal cache maintenance; it is not a server fault.
- On `GetNarInfo`, when the purge guard fires (narinfo in DB, NAR absent, no
  download job in flight), the cache re-attempts an upstream fetch and serves the
  narinfo (HTTP 200) when upstream still has it.
- When the narinfo is genuinely unavailable upstream, the request resolves to
  `storage.ErrNotFound` (HTTP 404) so Nix falls back to its next substituter —
  never a 500.
- The HTTP narinfo handler treats `errNarInfoPurged` (if it ever reaches the
  handler) as a 404, not a 500, as a defense-in-depth backstop.

## Capabilities

### New Capabilities

- `narinfo-purge-serving`: Defines the client-facing serving outcome when the
  narinfo purge guard fires. The purge sentinel is never a 500; a fired purge
  triggers an upstream re-fetch that yields 200 when available or 404 (upstream
  fallback) when not. Complements `narinfo-concurrent-fetch` (which governs the
  in-flight concurrent case) and `nar-cache-miss-recovery` (which governs NAR
  records).

### Modified Capabilities

<!-- None. Existing specs cover adjacent behavior but not the purge-sentinel-as-500 outcome. -->

## Impact

- **Code**: `pkg/cache/cache.go` (`GetNarInfo` purge/re-fetch flow, propagation of
  `errNarInfoPurged`); `pkg/server/server.go` narinfo GET handler error mapping.
- **API**: Eliminates the `HTTP 500: the narinfo was purged` response for the
  affected hashes. Affected requests now return 200 (served) or 404 (upstream
  fallback). Not breaking — strictly removes an erroneous error response.
- **Network/latency**: A fired purge adds one upstream narinfo round-trip to the
  request that triggered it (previously that request 500'd). No change on the
  cache-hit path. No measurable change to NAR streaming throughput.
- **Memory**: None — no new buffering or pooling.

## Non-goals

- Self-healing or batch reconciliation of the existing 3013 orphaned narinfos /
  2507 orphaned nar_files backlog (left to fsck and a future change).
- Investigating *why* NARs go missing (background NAR download failures under
  distributed-lock contention between replicas) — tracked separately.
- Any change to NAR (`/nar/...`) serving semantics, which `nar-cache-miss-recovery`
  already covers.
- Schema or migration changes.
