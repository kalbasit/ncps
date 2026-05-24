## Problem

When lazy CDC chunking is enabled, ncps stores narinfos with their upstream compression
(e.g., `Compression: xz`, URL `nar/hash.nar.xz`). After the NAR is migrated to CDC
chunks (which are always stored uncompressed), `GetNarInfo` still returns the stale
narinfo with `Compression: xz`.

When Nix subsequently downloads the `.nar.xz` URL, ncps serves **uncompressed** data
from CDC chunks, while Nix expects xz-compressed data. Nix fails with:

```
error: input compression not recognized
```

## Root Cause

In `GetNarInfo` (`pkg/cache/cache.go`), when a narinfo with `Compression: xz` is
fetched from the database, the code triggers a background migration
(`maybeBackgroundMigrateNarToChunks`) to update the narinfo URL asynchronously. However,
it returns the **stale** narinfo to the caller immediately — before the background
migration has a chance to update the DB.

The background cleanup (`migrateNarToChunksCleanup`) correctly updates the narinfo URL
in the DB, but there is a race window between:
1. `GetNarInfo` returning `Compression: xz` to Nix
2. `migrateNarToChunksCleanup` updating the DB to `Compression: none`

During this window, `GetNar` for the `.nar.xz` URL finds the NAR in chunks
(`HasNarInChunks` returns true via the `Compression=none` nar_file fallback) and serves
uncompressed data — causing the mismatch.

## Proposed Fix

In `GetNarInfo`, after triggering the background migration for a narinfo with non-none
compression in CDC mode, synchronously check if the NAR is already in chunks
(`HasNarInChunks`). If yes, normalize the narinfo URL in memory to `Compression: none`
before returning — eliminating the race window.

The DB update continues to happen asynchronously (reducing future query overhead once
the narinfo URL is fixed in the DB).

## Impact

- Fixes "input compression not recognized" errors in lazy-chunking CDC deployments
- Adds one extra DB query per `GetNarInfo` call for narinfos with non-none compression
  in CDC mode (only during the migration transition window)
- No schema changes, no migrations needed
