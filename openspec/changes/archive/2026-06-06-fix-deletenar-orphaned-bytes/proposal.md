## Why

`Cache.DeleteNar` removes only the exact URL variant via `c.narStore.DeleteNar(ctx, narURL)`, then clears the `bytes_stored_at` marker. But for `Compression:none` NARs the physical object is stored under the `.nar.zst` variant (the same asymmetry `deleteNarBytes` already compensates for). So deleting a none NAR (`nar/<H>.nar`) leaves the real `.nar.zst` blob orphaned on disk **and** clears `bytes_stored_at` — leaking storage and making the cleared marker untruthful: the `/upload` presence check reports the NAR absent while its bytes are still physically present.

## What Changes

- `Cache.DeleteNar` SHALL remove the NAR's physical object under the variant it is actually stored as — for a `Compression:none` NAR that means the `.nar.zst` variant (mirroring `statNarInStore`) — **before** clearing the `bytes_stored_at` marker.
- The established "deleting an absent NAR returns `ErrNotFound`" contract is preserved: a not-found error is returned (and the marker kept) only when **no** variant of the NAR was present.
- After a successful deletion, no orphaned physical object remains and the cleared `bytes_stored_at` marker is truthful (the `/upload` presence check and the NAR's on-disk state agree).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `upload-reference-presence`: add a requirement that NAR deletion removes the physical object for every compression variant before clearing the durable `bytes_stored_at` marker, so the marker never reports absent while bytes remain on disk.

## Non-goals

- No change to `deleteNarBytes` itself (it already handles the variant correctly).
- No change to how `bytes_stored_at` is set by `PutNar` or read on the `/upload` path.
- No retroactive sweep for already-orphaned `.nar.zst` blobs left by the current behavior; that would be a separate GC/fsck concern.

## Impact

- **Code**: `pkg/cache/cache.go` — `DeleteNar` (variant-aware delete: remove the `.nar.zst` variant for `none` NARs, track whether any variant was present, preserve the `ErrNotFound`-on-absent contract).
- **Database**: none (no schema/migration change).
- **I/O / network / memory**: negligible. At most one additional store `DeleteNar` call (the `.nar.zst` variant) per `Compression:none` eviction; `ErrNotFound` on a variant is tolerated. No change to the hot serve path.
