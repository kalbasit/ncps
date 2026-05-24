# Capability Spec: CDC Chunking

## Purpose

Defines requirements for Content-Defined Chunking (CDC) of NAR files, including ingestion
validation, error handling, and data integrity guarantees.

## Requirements

### Requirement: CDC ingestion MUST validate total byte count before committing
When CDC chunking completes (the chunker signals end-of-stream by closing `chunksChan`),
the system SHALL compare the total accumulated uncompressed bytes (`totalSize`) against
the narinfo's declared `NarSize` (`fileSize` parameter). If `fileSize > 0` and
`uint64(totalSize) != fileSize`, the system MUST:
- Return an error wrapping `io.ErrUnexpectedEOF` with a message that includes both the
  expected and actual byte counts.
- NOT call `UpdateNarFileTotalChunks` (leave `total_chunks = 0`, `chunking_started_at`
  set, so stale lock recovery handles cleanup after 1 hour).
- Log the mismatch at `error` level including the narinfo hash and both byte counts.

If `fileSize == 0`, the validation MUST be skipped (narinfo with unknown declared size).

#### Scenario: Early-EOF truncation is rejected at commit
- **WHEN** the decompressed NAR stream ends before `fileSize` bytes are consumed (e.g., upstream HTTP/2 stream drops with GOAWAY)
- **THEN** `storeNarWithCDCFromReader` returns a non-nil error
- **AND** the `nar_file` row retains `total_chunks = 0` (not committed as complete)
- **AND** an error-level log entry records the expected vs actual byte count

#### Scenario: Complete NAR is committed normally
- **WHEN** the decompressed NAR stream produces exactly `fileSize` uncompressed bytes before EOF
- **THEN** `storeNarWithCDCFromReader` calls `UpdateNarFileTotalChunks` with the correct count
- **AND** `nar_file.total_chunks > 0` after the call
- **AND** no error is returned

#### Scenario: NarSize is zero — validation is skipped
- **WHEN** `fileSize` parameter passed to `storeNarWithCDCFromReader` is 0
- **THEN** the size validation step is skipped entirely
- **AND** `UpdateNarFileTotalChunks` is called regardless of how many bytes were chunked

### Requirement: GetNarInfo MUST normalize compression in-memory during lazy-chunking transition

When lazy CDC chunking is enabled, narinfos are initially stored in the DB with the
upstream compression (e.g., `Compression: xz`) while the NAR is chunked in the
background. After chunking completes, the DB update is asynchronous. During this window,
`GetNarInfo` MUST normalize the narinfo in-memory before returning so that clients
receive consistent `Compression: none` / `.nar` URL without waiting for the DB update.

The normalization SHALL:
- Call `HasNarInChunks` (a lightweight indexed query checking `total_chunks > 0`)
- If the NAR is already chunked: rewrite `Compression`, `URL`, `FileHash`, and `FileSize`
  in memory only — the DB record is NOT modified synchronously
- Normalize the URL hash before the `HasNarInChunks` lookup to handle nix-serve-style
  prefixed hashes (e.g., `abc-hash` → `hash`) matching nar_file rows correctly
- Skip normalization entirely when CDC is disabled

#### Scenario: GetNarInfo normalizes when NAR is already chunked

- **GIVEN** CDC is enabled
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz`
- **AND** the NAR has been migrated to CDC chunks (`HasNarInChunks` returns true)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo has `Compression: none` and URL `nar/hash.nar`
- **AND** `FileHash` is nil and `FileSize` is 0
- **AND** the narinfo DB record is NOT modified synchronously

#### Scenario: GetNarInfo does NOT normalize when NAR is not yet chunked

- **GIVEN** CDC is enabled
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz`
- **AND** the NAR is NOT in CDC chunks (`HasNarInChunks` returns false)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo retains `Compression: xz` and the original URL
- **AND** background migration is triggered

#### Scenario: No normalization when CDC is disabled

- **GIVEN** CDC is disabled
- **AND** a narinfo exists in the DB with `Compression: xz`
- **WHEN** `GetNarInfo` is called
- **THEN** the returned narinfo retains the original `Compression: xz`

### Requirement: GetNar MUST return 404 for compressed URL when NAR exists only as chunks

When CDC is enabled and a client requests a NAR by its compressed URL (e.g.,
`nar/hash.nar.xz`), but the NAR exists only as CDC chunks (no whole-file in storage),
the system SHALL return `storage.ErrNotFound` rather than attempting to serve
uncompressed chunk data as compressed content. Serving chunk data with mismatched
compression would cause Nix to fail with "input compression not recognized".

#### Scenario: xz NAR request returns 404 when only chunks exist

- **GIVEN** CDC is enabled
- **AND** `nar_file.total_chunks > 0` (NAR is chunked)
- **AND** no whole-file `.nar.xz` exists in storage
- **WHEN** `GetNar` is called with a `.nar.xz` URL
- **THEN** the response is `storage.ErrNotFound`
- **AND** chunk data is NOT served

#### Scenario: xz NAR request succeeds when whole-file is still in storage

- **GIVEN** CDC is enabled with lazy chunking
- **AND** `nar_file.total_chunks > 0` (NAR is chunked)
- **AND** the whole-file `.nar.xz` still exists in storage (lazy chunking preserves it)
- **WHEN** `GetNar` is called with a `.nar.xz` URL
- **THEN** the whole-file xz content is served from storage

### Requirement: CDC goroutine error MUST be logged at error level
When the background CDC goroutine (started from `pullNarIntoStore`) receives a non-nil
error from `storeNarWithCDCFromReader`, it SHALL log the error at `error` level (not
`debug` or `warn`). The log entry MUST include the narinfo hash and NAR URL.

#### Scenario: Truncated CDC fails with visible log
- **WHEN** `storeNarWithCDCFromReader` returns a non-nil error inside the CDC goroutine
- **THEN** a log entry at level `error` is emitted with the NAR hash and error message
- **AND** no success log ("download of nar complete") is emitted for that NAR
