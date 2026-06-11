## ADDED Requirements

### Requirement: Reclaim chunks stranded behind stale links to dechunked nar_files

The system SHALL, during `fsck --repair` in CDC mode, delete `nar_file_chunks` links whose parent `nar_file` is dechunked (`total_chunks <= 0` and not actively chunking) and then reclaim the chunks left with no remaining links, deleting both their `chunks` DB rows and their chunk-store blobs. A chunked NAR (`total_chunks > 0`), its links, and its chunks MUST NOT be modified, and a chunk still referenced by any chunked NAR MUST be retained.

#### Scenario: stale link to a dechunked nar_file is removed and its chunk reclaimed

- **WHEN** a `nar_file` has `total_chunks = 0` (not actively chunking) yet still has a `nar_file_chunks` link to a chunk used by no other nar_file, and `fsck --repair` runs
- **THEN** the stale link is deleted and the chunk's DB row and storage blob are reclaimed

#### Scenario: chunked NAR is left intact

- **WHEN** a `nar_file` has `total_chunks > 0` with its chunk links and chunks, and `fsck --repair` runs
- **THEN** its links and chunks are not modified

#### Scenario: shared chunk retained while still referenced

- **WHEN** a chunk is linked by both a dechunked nar_file (stale) and a chunked (`total_chunks > 0`) nar_file
- **THEN** only the stale link is removed and the chunk is retained because it still has a live link

#### Scenario: idempotent

- **WHEN** `fsck --repair` runs again after a prior cleanup
- **THEN** no further links or chunks are removed
