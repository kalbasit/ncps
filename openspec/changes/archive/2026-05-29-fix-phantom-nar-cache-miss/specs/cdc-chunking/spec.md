## ADDED Requirements

### Requirement: Placeholder nar_file records MUST NOT be treated as servable

A `nar_file` row with `total_chunks = 0` and `chunking_started_at` NULL is a **placeholder**
created by `storeInDatabase` at narinfo-fetch time before any NAR bytes have been downloaded or
chunked. Such a placeholder SHALL NOT be considered servable by any read path. Specifically:

- The `hasAsset` callback used by `coordinateDownload`/`prePullNar` SHALL return `false` for a
  placeholder, so coordination does not return an already-completed (`closed`) download state.
- `serveNarFromStorageViaPipe` SHALL NOT be entered for a placeholder when no whole-file exists
  in the store and no chunks exist.

Only a whole-file in storage, `total_chunks > 0`, or chunking actively in progress within
`cdcChunkingLockTTL` makes the NAR servable.

#### Scenario: Placeholder is not reported as an available asset

- **GIVEN** CDC is enabled
- **AND** a `nar_file` placeholder row exists for hash `H` (`total_chunks = 0`, `chunking_started_at` NULL)
- **AND** no whole-file and no chunks exist for `H`
- **WHEN** `coordinateDownload`'s `hasAsset` is evaluated for `H`
- **THEN** it SHALL return `false`
- **AND** `coordinateDownload` SHALL proceed to download `H` rather than returning a completed state

#### Scenario: Placeholder does not cause a 2ms not-found

- **GIVEN** a `nar_file` placeholder row for hash `H` with no backing data
- **WHEN** `GetNar` is called for `H`
- **THEN** the system SHALL NOT call `serveNarFromStorageViaPipe` and return `storage.ErrNotFound`
  without first attempting an upstream download

### Requirement: Stuck chunking records MUST be recoverable

A `nar_file` row whose chunking was started but never completed (`total_chunks = 0` with
`chunking_started_at` older than `cdcChunkingLockTTL`, and no whole-file present) is **stuck**.
Stuck rows SHALL be recoverable: the CDC lazy-recovery job and/or the next `GetNar` SHALL
re-drive the download/chunking to completion or reset the row so a clean download can occur. A
stuck row SHALL NOT permanently cause reads to fail.

#### Scenario: Stuck record is re-driven by recovery

- **GIVEN** a `nar_file` row for hash `H` with `total_chunks = 0` and `chunking_started_at`
  older than `cdcChunkingLockTTL`
- **AND** no whole-file for `H` exists in the store
- **WHEN** the CDC lazy-recovery job processes `H`
- **THEN** it SHALL re-attempt download/chunking for `H` or reset the row for a clean retry
- **AND** after recovery a `GetNar` for `H` SHALL succeed if upstream has the NAR

#### Scenario: Stuck record served on demand

- **GIVEN** a stuck `nar_file` row for hash `H`
- **WHEN** a client requests `GET /nar/H...`
- **THEN** `GetNar` SHALL re-attempt the upstream download rather than returning a terminal 404
