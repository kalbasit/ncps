## ADDED Requirements

### Requirement: Fsck MUST detect CDC NARs whose stored size mismatches the declared NarSize
In CDC mode, `ncps fsck` SHALL query for `nar_file` rows where `total_chunks > 0` AND
`nar_files.file_size != narinfos.nar_size` (joined via `narinfo_nar_files`). Such rows
represent NARs that were committed as "complete" CDC artifacts but whose total stored
bytes do not match the narinfo's declared uncompressed size. These rows SHALL be reported
as a distinct issue category: "CDC NARs with size mismatch".

The check SHALL be implemented as a dedicated DB query (`GetCDCNarFilesWithSizeMismatch`)
on all three supported engines (SQLite, PostgreSQL, MySQL).

The `fsckResults` struct SHALL include a new field `narFilesWithSizeMismatch []database.NarFile`.
The `totalIssues()` method SHALL include `len(narFilesWithSizeMismatch)` in its count.

#### Scenario: Truncated CDC row is flagged by fsck
- **WHEN** a `nar_file` row has `total_chunks > 0` and `file_size = 490516` while the linked narinfo has `nar_size = 166983168`
- **THEN** `ncps fsck` reports it under "CDC NARs with size mismatch"
- **AND** the total issue count is incremented

#### Scenario: Correctly-chunked CDC row is not flagged
- **WHEN** a `nar_file` row has `total_chunks > 0` and `file_size` exactly equals the linked narinfo's `nar_size`
- **THEN** it does NOT appear in "CDC NARs with size mismatch"

#### Scenario: Non-CDC nar_file is not flagged by this check
- **WHEN** a `nar_file` row has `total_chunks = 0` (whole-file storage)
- **THEN** it is excluded from `GetCDCNarFilesWithSizeMismatch` regardless of file_size

### Requirement: Fsck --repair MUST delete size-mismatched CDC NARs
When `ncps fsck --repair` (or interactive repair) is executed and size-mismatched CDC
rows are present, the system SHALL delete those `nar_file` rows and cascade cleanup
using the same repair path as `narFilesWithChunkIssues`:
1. Delete the `nar_file` row (cascades `nar_file_chunks`).
2. Delete any narinfos that become orphaned as a result.
3. Run `GetOrphanedChunks` and delete newly-orphaned chunk DB records and chunk files.

After repair, a subsequent ncps request for those narinfos SHALL trigger a fresh upstream fetch.

#### Scenario: Repair deletes truncated CDC row and orphaned narinfo
- **WHEN** `ncps fsck --repair` runs against a DB with a size-mismatched CDC nar_file
- **THEN** the `nar_file` row is deleted
- **AND** any narinfos linked only to that nar_file are deleted
- **AND** chunk files no longer referenced by any nar_file are deleted from storage and DB

#### Scenario: Dry-run shows mismatch without deleting
- **WHEN** `ncps fsck --dry-run` runs against a DB with a size-mismatched CDC nar_file
- **THEN** the issue is reported in the summary
- **AND** no rows are deleted

### Requirement: Fsck summary table MUST include CDC size-mismatch row
The `printFsckSummary` function SHALL include a new row "CDC NARs w/ size mismatch:" in
the CDC section of the summary table when `cdcMode` is true, showing the count of
size-mismatched rows with the standard ✅/❌ indicator.

#### Scenario: Zero mismatches shows green checkmark
- **WHEN** no size-mismatched CDC rows exist
- **THEN** the summary row shows `0` with ✅

#### Scenario: Nonzero mismatches shows red cross
- **WHEN** one or more size-mismatched CDC rows exist
- **THEN** the summary row shows the count with ❌
