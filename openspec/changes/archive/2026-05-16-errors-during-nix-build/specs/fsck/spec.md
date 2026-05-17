## ADDED Requirements

### Requirement: Fsck --verify-content MUST hash each CDC chunk's decompressed content against its stored hash
When `--verify-content` is set, `ncps fsck` SHALL read each chunk via `GetChunk` (which decompresses from zstd), stream the output through a SHA-256 hash, and compare the digest against the chunk hash stored in the database. Any NAR file with at least one chunk whose digest does not match its stored key SHALL be added to the `narFilesWithCorruptChunks` category.

#### Scenario: Corrupt chunk content is detected
- **WHEN** `--verify-content` is set and a chunk's decompressed bytes do not hash to its stored key
- **THEN** the owning `nar_file` is reported under "NAR files w/ corrupt chunks"
- **AND** the total issue count is incremented

#### Scenario: All chunks pass content verification
- **WHEN** `--verify-content` is set and all chunks decompress and hash to their stored keys
- **THEN** the NAR file is not flagged under "NAR files w/ corrupt chunks"

#### Scenario: Content verification is skipped without the flag
- **WHEN** `--verify-content` is NOT set
- **THEN** `ncps fsck` performs no chunk content reads
- **AND** `narFilesWithCorruptChunks` is always empty and omitted from the summary

### Requirement: Fsck --verify-content MUST verify the assembled NAR hash against the narinfo NarHash
When `--verify-content` is set and all individual chunk hashes pass for a given NAR file, `ncps fsck` SHALL concatenate the decompressed chunk bytes in index order, compute the SHA-256 digest of the assembled stream, and compare it against the narinfo `NarHash` field. Any NAR file whose assembled digest does not match SHALL be added to the `narFilesWithHashMismatch` category.

#### Scenario: Assembled NAR hash matches narinfo NarHash
- **WHEN** all chunks pass individual hash verification and the assembled SHA-256 matches the narinfo NarHash
- **THEN** the NAR file is not flagged under "NAR files w/ hash mismatch"

#### Scenario: Assembled NAR hash does not match narinfo NarHash
- **WHEN** all individual chunk hashes pass but the assembled SHA-256 does not match the narinfo NarHash
- **THEN** the NAR file is reported under "NAR files w/ hash mismatch"
- **AND** the total issue count is incremented

#### Scenario: NAR-level hash check is skipped when any chunk is corrupt
- **WHEN** a chunk fails content verification for a given NAR file
- **THEN** the end-to-end NAR hash check is skipped for that NAR
- **AND** only "NAR files w/ corrupt chunks" is incremented, not "NAR files w/ hash mismatch"

### Requirement: Fsck --repair MUST delete corrupt-chunk and hash-mismatch CDC NARs
When `--repair` is set, `ncps fsck` SHALL delete `nar_file` records flagged in `narFilesWithCorruptChunks` and `narFilesWithHashMismatch`, cascade to orphaned narinfos, and remove orphaned chunk DB records and chunk files from storage — using the same cascade as `narFilesWithChunkIssues`.

#### Scenario: Repair deletes corrupt CDC NAR and cascades
- **WHEN** `--repair` is set and a NAR file has corrupt chunks
- **THEN** the `nar_file` DB record is deleted
- **AND** the linked narinfo is deleted if it becomes orphaned
- **AND** orphaned chunk DB records and chunk files in storage are removed

#### Scenario: Dry-run shows corrupt and hash-mismatch NARs without deleting
- **WHEN** `--dry-run` is set
- **THEN** corrupt-chunk and hash-mismatch NARs are reported in the summary
- **AND** no records or files are deleted

### Requirement: Fsck summary table MUST include corrupt-chunk and hash-mismatch rows when --verify-content is set
When `--verify-content` is active, the fsck CDC summary section SHALL include two additional rows: "NAR files w/ corrupt chunks" and "NAR files w/ hash mismatch".

#### Scenario: Zero corrupt-chunk NARs shows green checkmark
- **WHEN** `--verify-content` is set and no corrupt-chunk NARs exist
- **THEN** the "NAR files w/ corrupt chunks" summary row shows `0` with ✅

#### Scenario: Nonzero corrupt-chunk NARs shows red cross
- **WHEN** `--verify-content` is set and corrupt-chunk NARs are found
- **THEN** the "NAR files w/ corrupt chunks" summary row shows the count with ❌

#### Scenario: Rows are omitted when --verify-content is not set
- **WHEN** `--verify-content` is NOT set
- **THEN** the summary table does not include "NAR files w/ corrupt chunks" or "NAR files w/ hash mismatch" rows
