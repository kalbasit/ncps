## Context

CDC stores a NAR's data as `chunks` rows linked to a `nar_file` via `nar_file_chunks`, with the parent `nar_file.total_chunks > 0`. When a NAR is dechunked (migrate-chunks-to-nar) or chunking is abandoned, `total_chunks` is reset to 0 but the `nar_file_chunks` links were not always removed — leaving stale links. fsck (`pkg/ncps/fsck.go`) reclaims orphaned chunks via `entchunk.Not(entchunk.HasNarFileLinks())`, so a chunk still referenced by a stale link is not orphaned and survives forever. `detectFsckCDCMode` already re-enables CDC mode on chunk residue (#1364), so the read/repair phases run; the gap is purely the stale-link case.

## Goals / Non-Goals

**Goals:**
- Reclaim chunks stranded behind `nar_file_chunks` links to dechunked (`total_chunks = 0`) nar_files: delete the stale links, then delete the now-orphaned chunk DB rows and storage blobs.
- Safe and idempotent; never touch a chunked (`total_chunks > 0`) NAR.

**Non-Goals:**
- Changing how chunking/dechunking writes links (prevention is out of scope; this reclaims existing residue).
- The already-handled unlinked-orphan reclamation and chunk-count-mismatch repairs.

## Decisions

- **Define stale links by the parent's `total_chunks <= 0`.** A dechunked nar_file must own no chunk links, so `nar_file_chunks` whose `nar_file.total_chunks <= 0` are unambiguously stale. Delete them, then reclaim chunks via the existing `Not(HasNarFileLinks())` predicate. *Alternative:* delete chunks directly by walking links — rejected: a chunk may be shared (dedup) by a still-chunked nar_file; deleting only after it has no remaining links is the safe order.
- **Reclaim within the same repair function.** The orphaned-chunk suspect list is collected in the read phase, before this deletion, so newly-orphaned chunks must be re-queried and deleted here (mirrors `repairSizeMismatchCDCNarFiles`, which re-queries orphaned chunks after its nar_file deletions). *Alternative:* rely on a second fsck pass — rejected: one `--repair` run should converge.
- **Gate on CDC mode + repair, like the other chunk repairs.** Reuses the existing `repair && cdcMode && chunkStore != nil` guard around `repairFsckIssues`.

## Risks / Trade-offs

- **Deleting a chunk shared with a still-chunked NAR** → prevented: chunks are deleted only when they have NO remaining `nar_file_chunks` links after stale-link removal, so a chunk still referenced by a `total_chunks > 0` nar_file is retained.
- **Large delete volume (millions of rows)** → bounded by batched deletes; runs under `--repair` which operators invoke deliberately. Storage-blob deletion tolerates already-absent blobs (ErrNotFound non-fatal), matching existing reclamation.
- **Mis-classifying an in-progress chunking NAR** → excluded: in-progress chunking has `chunking_started_at` set with `total_chunks = 0`, but a freshly-chunking NAR's links are being written, not stale. To be safe the cleanup only removes links whose parent is dechunked AND not actively chunking (chunking_started_at NULL), leaving mid-chunking NARs alone.

## Migration Plan

Ships as fsck repair logic; operators run `ncps fsck --repair` once to reclaim the residue. No DB migration. Rollback: revert the binary; reclaimed rows/blobs were unreferenced.

## Open Questions

- None blocking.
