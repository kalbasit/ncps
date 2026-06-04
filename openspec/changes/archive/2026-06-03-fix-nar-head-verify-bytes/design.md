## Context

The NAR `HEAD` handler `getNar(false)` (pkg/server/server.go) has a fast-path
optimization: it calls `c.GetNarFileSize(nu)` and, if that returns `size > 0`,
writes `200` with `Content-Length` and returns — **without** confirming the NAR
bytes exist. `GetNarFileSize` (pkg/cache/cache.go:1430) reads the `nar_file` row
via `getNarFileFromDB`; it is a pure DB lookup.

Every other path determines NAR existence from actual bytes:
`statNarInStore` (cache.go:3989, tri-state present/absent/ambiguous) and
`HasNarInChunks` (cache.go:7066). The `nar-cache-miss-recovery` capability already
mandates that a bare `nar_file` row does not make a NAR servable — but only the
`GetNar` path enforces it.

So a phantom (`nar_file` row, no bytes) makes `HEAD` return `200`. During
`nix copy --to .../upload`, nix HEADs each NAR before uploading; a false `200`
makes nix skip the NAR upload (nix uploads NAR-first, narinfo-last), leaving a
phantom, and a dependent's reference-verification `GET` then `404`s → Lix aborts.
Confirmed deterministically at `replicas=1`.

Exported helpers available to the server: `HasNarInStore(bool)` (swallows the
ambiguous error → returns false) and `HasNarInChunks(bool, error)`. The tri-state
`statNarInStore` is unexported.

## Goals / Non-Goals

**Goals:**
- NAR `HEAD` reflects real servability, consistent with `GetNar`.
- A record-without-bytes NAR HEADs `404` on `/upload` so nix re-uploads.
- Honor the tri-state stat: an ambiguous storage error is neither a false `200`
  nor a false `404`.
- Keep the fast path for the common case (servable NAR → cheap stat → `200`).

**Non-Goals:**
- Write-path seed prevention (`storeInDatabase` creating byteless `nar_file`
  rows) — separate follow-up.
- Changing `GetNar`, the narinfo paths, schema, or routes.
- Replica/topology concerns (irrelevant — repros at one replica).

## Decisions

### Decision: Gate the HEAD size-shortcut on actual servability via a new tri-state cache method

Add an exported `Cache.IsNarServable(ctx, narURL) (bool, error)` that mirrors
`GetNar`'s servability determination, tri-state:

- whole-file present (`statNarInStore` → true) → `(true, nil)`
- else chunks present (`HasNarInChunks` → true) → `(true, nil)`
- both confirmed absent → `(false, nil)`
- ambiguous storage error (stat returned an error) → `(false, err)`

In `getNar(false)`, keep `GetNarFileSize` for the `Content-Length` value, but emit
`200` only when `IsNarServable` returns `(true, nil)`:

```go
if !withBody {
    nu.TransparentZstd = false
    size, sErr := s.cache.GetNarFileSize(r.Context(), nu)
    if sErr == nil && size > 0 {
        if servable, _ := s.cache.IsNarServable(r.Context(), nu); servable {
            // write 200 + Content-Length, return
        }
    }
    // not servable / ambiguous / no record → fall through to GetNar
}
nu, size, reader, err := s.cache.GetNar(...)   // existing path
```

**Why fall through to `GetNar` for the non-servable case** instead of writing a
bare `404`: `GetNar` already encodes the correct, divergent behavior the spec
requires — on `cache.IsUploadOnly` it returns `storage.ErrNotFound` (→ `404`, so
nix re-uploads), and on the substituter path it attempts upstream recovery. The
HEAD handler already tolerates this path for HEAD (it writes headers only when
`!withBody`), so HEAD stays consistent with GET by construction. The ambiguous
case also falls through, where `GetNar` handles it without purging.

**Alternatives considered:**
- *Gate on `HasNarInStore || HasNarInChunks` (existing exported helpers).* Works,
  but `HasNarInStore` collapses the ambiguous error to `false`, so an ambiguous
  stat would drop out of the fast path into `GetNar` — acceptable, but a dedicated
  tri-state method states intent and keeps the ambiguous decision explicit.
- *Make `GetNarFileSize` verify bytes.* Rejected: it is used elsewhere purely for
  the size value; overloading it with a presence check risks unrelated callers.
- *Write `404` directly when not servable.* Rejected: it would diverge HEAD from
  GET on the substituter path (no recovery) and duplicate the upload-only logic.

## Risks / Trade-offs

- **Extra cost on HEAD**: one storage `stat` (and a chunk check only when the
  whole-file is absent) per HEAD, replacing a DB-only answer. Bounded, no NAR
  bytes read; HEAD is an infrequent verb. Net work drops (no more failed-copy
  retries / phantom churn). → Acceptable.
- **Substituter HEAD on a missing NAR now falls through to `GetNar`**, which may
  trigger upstream recovery to answer HEAD. This matches GET semantics (HEAD
  should reflect availability) and is the documented behavior; the hot
  `/upload` case returns `404` cheaply (no upstream). → Acceptable.
- **Ambiguous storage error drops HEAD out of the fast path** into `GetNar`. Same
  handling as GET; no purge, no false 404. → Acceptable.

## Migration Plan

- Pure behavioral change; no schema, migration, config, or route change.
- Deploy: ship the image. Rollback: revert the commit.
- Existing phantom rows need no special handling — once HEAD reports them absent,
  nix re-uploads (or substituter recovery heals them).

## Open Questions

- None blocking. (Whether to also short-circuit `404` for ambiguous on the
  upload-only path is deferred to `GetNar`'s existing behavior; revisit only if a
  HEAD-specific ambiguous case proves problematic in practice.)
