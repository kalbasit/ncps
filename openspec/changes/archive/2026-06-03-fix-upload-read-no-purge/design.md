## Context

`GetNarInfo` (pkg/cache/cache.go) has a "purge guard": when a narinfo row exists
but its backing NAR is absent from storage and no download is in flight, it calls
`purgeNarInfo` (cache.go:~4357) — a destructive DB delete of the narinfo +
`nar_file` rows plus store deletes — and returns the internal `errNarInfoPurged`
sentinel. `GetNarInfo` then routes that sentinel: on the root/substituter path it
falls through to an upstream re-fetch; on the `/upload` path
(`cache.IsUploadOnly(ctx)`) the short-circuit at cache.go:~3432 returns
`storage.ErrNotFound` (HTTP 404).

The destructive purge runs on **both** paths. On the upload path this makes the
cache's "is path X present?" answer non-monotonic within a single `nix copy`: the
narinfo is present when the client runs `queryValidPaths`, then deleted by a
purge-on-read before the per-reference verification step, so Lix's
`BinaryCacheStore::addToStore` aborts with "the reference does not exist". Each
retry re-`PUT`s the narinfo (re-seeding a phantom) which is purged again — a churn
loop (244 purge events in one production run).

Constraints: the project mandates TDD; the change must be additive/behavioral only
(no schema or migration); root-path self-healing behavior must be preserved.

## Goals / Non-Goals

**Goals:**
- Make `/upload` narinfo reads non-destructive so `nix copy --to .../upload`
  succeeds against a cache containing phantom narinfos.
- Preserve identical root/substituter purge-and-re-fetch behavior.
- Keep the diff surgical and well-covered by tests.

**Non-Goals:**
- Preventing phantom *seeds* at write time (`PutNarInfo`/`storeInDatabase` creating
  a `nar_file` record without verifying NAR bytes) — separate change.
- Bulk cleanup of existing phantom narinfos (fsck/operational).
- Any change to CDC chunk handling or replica/storage topology.

## Decisions

### Decision: Skip only the `purgeNarInfo` call under upload-only; reuse the existing sentinel routing

In the purge-guard block of `getNarInfoFromDatabase`, branch on
`IsUploadOnly(ctx)` immediately **before** the `purgeNarInfo` call. When
upload-only, return the existing `errNarInfoPurged` sentinel **without** purging:

```go
if IsUploadOnly(ctx) {
    // Upload-only reads are non-destructive. Report the missing-NAR narinfo as a
    // cache miss so the client re-uploads, but do NOT purge — a purge here makes
    // the cache's path-validity answer non-monotonic within a single `nix copy`
    // and aborts the client's reference-verification step.
    return nil, ErrNarInfoPurged
}
// existing: log "...requesting a purge" + purgeNarInfo(...) + return ErrNarInfoPurged
```

**Why:** `GetNarInfo` already maps `errNarInfoPurged` to `storage.ErrNotFound`
(HTTP 404) on the upload-only path (cache.go:~3432) and to upstream re-fetch on the
root path. Returning the same sentinel from the upload-only branch yields the
desired 404 with the smallest possible change and no new control flow in
`GetNarInfo`. The sentinel is internal and never reaches the client (defense-in-
depth handler mapping already covers a leak).

**Alternatives considered:**
- *Return `storage.ErrNotFound` directly from the guard.* Rejected: it would enter
  the `!errors.Is(err, ErrNarInfoPurged)` branch (cache.go:~3416) and
  `handleStorageFetchError`, a different code path with its own retry semantics —
  more surface area and behavior to reason about than reusing the established
  sentinel route.
- *Gate the purge at the HTTP/server layer.* Rejected: `IsUploadOnly` is a cache
  context concern and the guard lives in the cache; the server handler has no
  visibility into the guard firing.

## Risks / Trade-offs

- **Stale phantom narinfo persists on the upload path** → Acceptable and intended:
  the immediately following client `PUT` overwrites it (`storeInDatabase` upserts).
  Root-path reads still purge-and-re-fetch, so substituter consumers self-heal.
- **`errNarInfoPurged` returned without an actual purge is mildly misleading** →
  Mitigated by a clear comment at the branch; the sentinel is internal-only and the
  routing it triggers (→ 404) is exactly what's wanted.
- **A concurrent root-path read could still purge the same hash mid-copy** →
  Out of this change's control and unchanged by it; the upload path no longer
  *contributes* to the churn, which removes the dominant cause observed in prod.

## Migration Plan

- Pure behavioral change; no DB migration, no config, no API/route change.
- Deploy: ship the new image; both replicas pick it up on rollout.
- Rollback: revert the commit / redeploy the prior image — no state to undo.
- Existing phantoms need no special handling: upload reads stop deleting them,
  root reads continue to self-heal them, and re-uploads repair them.

## Open Questions

- None blocking. (Phantom-seed prevention at write time is acknowledged as a
  separate follow-up, not required for this fix to resolve the `nix copy` abort.)
