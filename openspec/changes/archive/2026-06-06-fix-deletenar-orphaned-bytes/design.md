## Context

`Cache.DeleteNar` (`pkg/cache/cache.go`) evicts a NAR: it deletes the store object and clears the durable `bytes_stored_at` marker on the matching `nar_file` row. The store, however, persists `Compression:none` NARs physically under the `.nar.zst` variant (whole-file zstd, served with transparent HTTP encoding). The codebase already encodes this asymmetry in two places:

- `statNarInStore` checks the `.nar.zst` variant when asked about a `none` URL.
- `deleteNarBytes` deletes the `.nar.zst` variant first, then the requested URL, "otherwise a reclaimed none NAR would leave its real object orphaned and invisible to normal cleanup/accounting."

`DeleteNar` predates / bypasses that helper: it calls `c.narStore.DeleteNar(ctx, narURL)` directly. For a `none` URL that deletes `nar/<H>.nar` (which does not exist) and leaves the real `.nar.zst` blob, then clears `bytes_stored_at`.

## Goals / Non-Goals

**Goals:**
- After `DeleteNar`, no physical NAR object for that hash remains in the store, regardless of compression.
- The cleared `bytes_stored_at` marker is truthful: `/upload` presence and on-disk state agree.

**Non-Goals:**
- Changing `deleteNarBytes`, `PutNar`, or the `/upload` read path.
- Sweeping pre-existing orphaned `.nar.zst` blobs (separate GC/fsck task).

## Decisions

**1. Delete the actually-stored variant and track whether anything was removed.**
For a `Compression:none` URL, delete the `.nar.zst` variant (the real object) as well as the bare none URL; for explicit compression, delete the URL itself. Tolerate `storage.ErrNotFound` on each individual delete but record whether **any** variant was present, and return `ErrNotFound` only when none was — mirroring `statNarInStore`, which reports a `none` NAR present if either the `.nar.zst` variant or the bare object exists. This preserves the established, tested contract ("`DeleteNar` of an absent NAR returns `ErrNotFound`") while removing the `.nar.zst` blob that the old single-URL delete orphaned.
*Alternatives considered*: calling `deleteNarBytes` directly (rejected — for a `none` URL it also deletes the bare object that never exists, so it **always** returns `ErrNotFound` even after deleting the real `.nar.zst`, conflating the found-signal and breaking the contract); blanket-tolerating `ErrNotFound` (rejected — silently turns the "delete of an absent NAR errors" contract into a success and would clear the marker for a genuinely-absent NAR); deleting the marker before the object (rejected — widens the window where the marker lies).

**2. Keep the ordering: delete bytes first, then clear the marker.**
Return on any non-not-found failure (and on a confirmed full absence) before clearing `bytes_stored_at`, so the marker continues to reflect reality (present) rather than being cleared while bytes survive or while the state is in doubt.

## Risks / Trade-offs

- [The `.nar.zst` variant is genuinely absent for a real `none`-stored NAR] → each individual delete tolerates `storage.ErrNotFound`; the `deleted` flag and the final not-found return distinguish "one variant gone" from "nothing was there."
- [Other `DeleteNar` call sites rely on the old single-variant behavior] → the public `Cache.DeleteNar` is the eviction entry point; deleting the stored variant is strictly more correct for every caller, and the `ErrNotFound`-on-absent contract is unchanged. Internal store-level `c.narStore.DeleteNar` calls elsewhere are untouched.

## Migration Plan

Pure code change; no schema or migration. Deployable immediately. Rollback is reverting the one-line change. Already-orphaned blobs from the prior behavior are unaffected (out of scope).

## Open Questions

None.
