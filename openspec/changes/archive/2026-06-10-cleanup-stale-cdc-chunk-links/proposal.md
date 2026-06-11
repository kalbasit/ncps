## Why

A live cache accumulated millions of orphaned CDC chunk rows: `chunks` ≈ 5.5M and `nar_file_chunks` ≈ 8164, yet **zero** `nar_files` have `total_chunks > 0`. fsck's orphaned-chunk reclamation only deletes chunks with NO `nar_file_chunks` link (`Not(HasNarFileLinks())`); the ~8164 stale links point to **dechunked** (`total_chunks = 0`) nar_files, so those chunks are never considered orphaned and are never reclaimed. `collectNarFilesWithChunkIssues` only inspects `total_chunks > 0` rows, so the stale links also escape there. The result is unbounded Postgres bloat that drain/fsck cannot clear.

## What Changes

- `ncps fsck --repair` SHALL delete `nar_file_chunks` links whose parent `nar_file` is dechunked (`total_chunks <= 0`) — a dechunked NAR must own no chunk links — and then reclaim the chunks left orphaned by that deletion (DB rows and chunk-store blobs), mirroring the existing orphaned-chunk reclamation.
- A chunked NAR (`total_chunks > 0`) and its links/chunks MUST NOT be touched.
- The cleanup runs whenever fsck is in CDC mode, including the post-drain chunk-residue mode (#1364), so it reclaims residue even with CDC disabled.

## Capabilities

### New Capabilities
- `cdc-chunk-residue-cleanup`: fsck reclamation of chunks stranded behind stale `nar_file_chunks` links to dechunked nar_files (`total_chunks = 0`), which the orphaned-chunk (unlinked) reclamation cannot reach.

### Modified Capabilities
<!-- none -->

## Impact

- `pkg/ncps/fsck.go`: new `reclaimStaleChunkLinks`, invoked from `repairFsckIssues` under `--repair` + CDC mode; counts logged.
- New unit test in `pkg/ncps`.
- Repair-only; read-only fsck unchanged. No schema/migration/API change. Reclaims significant DB + storage space.
