## Context

ncps supports two NAR storage modes:

1. **Whole-file**: NARs stored as compressed files (e.g., `.nar.xz`) in the storage
   backend. Narinfos reference the original compression.
2. **CDC (content-defined chunking)**: NARs are split into content-addressed chunks,
   always stored **uncompressed**. Narinfos should reference `Compression: none`.

When **lazy CDC chunking** is enabled, narinfos fetched from upstream are initially
stored with the upstream's compression (e.g., `Compression: xz`). This allows the NAR
to be served from the original compressed file while chunking happens in the background.

After chunking completes, `migrateNarToChunksCleanup` updates the narinfo in the DB to
`Compression: none`. However, `GetNarInfo` returns the stale narinfo immediately and
asynchronously triggers the cleanup — creating a race window.

During this window, `GetNar` for the `.nar.xz` URL detects the NAR is in chunks
(`HasNarInChunks` returns true via `getNarFileFromDB`'s `Compression=none` fallback) and
serves uncompressed data from chunks. The Nix client expects xz data and fails with
"input compression not recognized".

## Goals / Non-Goals

**Goals:**
- Eliminate "input compression not recognized" errors in lazy-chunking CDC deployments
- Fix the race between narinfo DB update and NAR serving
- Maintain backward compatibility with non-CDC and non-lazy-chunking deployments

**Non-Goals:**
- Fix the secondary "expected N chunks but got 0" data inconsistency (separate issue)
- Change the async narinfo DB update mechanism
- Add new configuration options

## Decisions

### Decision 1: Normalize narinfo in-memory in `GetNarInfo` when NAR is chunked

**Chosen**: In `GetNarInfo`, after triggering background migration for a narinfo with
non-none compression in CDC mode, call `HasNarInChunks`. If the NAR is already chunked,
normalize the narinfo URL and Compression field in memory before returning.

**Rationale**:
- Fixes the race window without changing the async DB update mechanism
- The in-memory normalization is cheap (one DB query) and only runs during the
  transition window (while narinfo URL hasn't been updated in DB yet)
- Once the DB is updated by `migrateNarToChunksCleanup`, `GetNarInfo` returns the
  correct narinfo directly without the extra `HasNarInChunks` query

**Alternative considered**: Return 404 from `GetNar` when compression mismatch is
detected (CDC chunks exist but narinfo says xz). Rejected: this would cascade to Nix
falling back to building, which is far worse than a transient normalization query.

**Alternative considered**: Re-compress from chunks to xz before serving. Rejected:
extremely expensive for large NARs; defeats the purpose of CDC chunking.

**Alternative considered**: Always normalize narinfo URL in `GetNarInfo` when CDC is
enabled (regardless of whether NAR is chunked). Rejected: if the NAR is NOT yet chunked
(still in whole-file xz storage), normalizing the URL would make `GetNar` unable to find
the NAR in `HasNarInStore` (which only checks `.nar` and `.nar.zst`, not `.nar.xz`),
forcing a wasteful re-download from upstream.

### Decision 2: No re-signing after in-memory normalization

The narinfo fingerprint used for signing covers: StorePath, NarHash, NarSize, References
— NOT URL or Compression. Therefore, modifying URL and Compression in memory does not
invalidate existing signatures. No re-signing is needed.

### Decision 3: FileHash and FileSize set to nil/0 when normalizing

`Compression: none` narinfos in ncps don't carry FileHash or FileSize (set in
`PutNarInfo` and `pullNarInfo` when normalizing). The in-memory normalization follows
the same convention for consistency.

## Risks / Trade-offs

**[Risk] Extra DB query during transition window** → Acceptable: `HasNarInChunks` is a
lightweight indexed query. The transition window (narinfo with xz URL in DB, NAR already
chunked) is transient and ends once `migrateNarToChunksCleanup` runs.

**[Risk] HasNarInChunks returns true for TotalChunks=1 but no NarFileChunk records** →
In this case, `GetNar` would still fail with "expected N chunks but got 0". This is the
pre-existing bug 2 (separate issue). Our fix changes the error path (Nix gets HTTP 404
instead of "input compression not recognized"), which is less confusing.

**[Risk] Narinfo served from storage (not DB) path** → If a narinfo is read from the
old storage backend (legacy path, not from DB), it won't go through the normalization
fix. However, these legacy narinfos have their NAR in whole-file storage too, so
`GetNar` would serve the xz file correctly — no bug in this path.

## Migration Plan

1. Deploy the code change — no DB migrations or config changes needed
2. In-flight requests during deployment may briefly see the old behavior; subsequent
   requests will normalize correctly
3. Rollback: revert the code change; behavior reverts to the pre-fix state (the race
   window returns, but no worse than before)
