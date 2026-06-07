## Why

`nix copy --to https://<host>/upload <closure>` aborts with *"cannot add 'X' to the binary cache because the reference 'Y' does not exist"* whenever the closure contains a phantom narinfo (narinfo row present, backing NAR bytes absent). On the upload path the purge guard **destructively deletes** the narinfo on read, so the destination's view of "is path Y valid?" flips from present (Nix's `queryValidPaths` phase) to absent (Nix's per-reference verification phase). Lix's `BinaryCacheStore::addToStore` reference check turns that non-monotonic answer into a fatal abort. Retrying re-`PUT`s the narinfo, which re-seeds the phantom, which is purged again — observed churn of 244 purge events in a single production run.

## What Changes

- When the narinfo purge guard fires **and** the request context is `cache.IsUploadOnly` (the `/upload` route), `GetNarInfo` MUST NOT call `purgeNarInfo`. It returns `storage.ErrNotFound` instead, so the client receives `HTTP 404` and proceeds to `PUT` (which overwrites the stale record).
- Purge-on-read behavior on the root/substituter path is **unchanged** — it still purges and re-fetches/falls back as today. Self-healing only matters where reads are substituter lookups; on `/upload` the client is the source of truth and is about to re-upload.
- Net effect: upload-path reads become **non-destructive and idempotent**, restoring the monotonic path-validity that `nix copy` relies on.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `narinfo-purge-serving`: add a requirement that the purge guard MUST skip the destructive purge and resolve to `404` (not delete, not upstream-fetch) when the request is upload-only. The existing root-path purge+re-fetch requirements are retained unchanged.

## Impact

- **Code**: `pkg/cache/cache.go` — the purge-guard block in `GetNarInfo` (~`cache.go:4300`, `purgeNarInfo` call at ~`4357`) gains an `IsUploadOnly(ctx)` branch. `pkg/cache` tests. No HTTP handler, schema, migration, or storage-layer changes.
- **Behavior**: only the `/upload` read path changes; root path identical. No API/route changes.
- **I/O / network / memory**: strictly reduces work on the upload path — eliminates a DB delete transaction and store-delete per phantom read, and avoids the re-PUT→re-purge churn. No new allocations on the hot NAR-streaming path. No measurable latency or memory change to normal serving.

## Non-goals

- Preventing phantom seeds at write time (`PutNarInfo`/`storeInDatabase` creating a `nar_file` record without verifying NAR bytes) — deliberately out of scope; tracked separately.
- Cleaning up the existing population of phantom narinfos (an fsck/purge pass) — operational, not part of this change.
- Any change to root/substituter purge-on-read, CDC chunk reassembly, or replica/storage topology.
