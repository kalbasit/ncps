# Capability Spec: Fsck

## Purpose

Defines requirements for `ncps fsck`, the consistency-checking and repair tool that
inspects the database and storage for integrity issues including orphaned records,
missing files, and CDC size mismatches.
## Requirements
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
The system SHALL, when `ncps fsck --repair` (or interactive repair) is executed and
size-mismatched CDC rows are present, delete those `nar_file` rows and cascade cleanup
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

### Requirement: Fsck CDC size-mismatch detection MUST scale beyond database driver parameter limits

The implementation of CDC size-mismatch detection (currently `queryCDCNarFilesWithSizeMismatch`) SHALL NOT issue any single SQL statement whose
bound-parameter count grows unboundedly with the number of CDC `nar_file` rows or
their joined `narinfo_nar_files`/`narinfos` rows. In particular, it MUST NOT produce
an `IN ($1...$N)` predicate (whether emitted directly or via an ORM eager-load) where
N can exceed the PostgreSQL extended-protocol limit of 65535 parameters.

Detection SHALL iterate in bounded batches (chunk size chosen so that
`batch_size * params_per_row` stays well below 65535 across all supported drivers).
The fsck phase 1g run MUST complete successfully against caches of arbitrary size.

The behavioral contract of the existing "Fsck MUST detect CDC NARs whose stored size
mismatches the declared NarSize" requirement is unchanged: the same rows are flagged,
in the same category, with the same `totalIssues()` accounting. Only the execution
strategy changes.

#### Scenario: Phase 1g completes against a large CDC cache on PostgreSQL

- **WHEN** `ncps fsck` runs against a PostgreSQL database containing more CDC
  `nar_file` rows than the PostgreSQL extended-protocol parameter limit (65535)
- **THEN** phase 1g ("checking CDC NAR files with size mismatch") completes without
  returning `extended protocol limited to 65535 parameters`
- **AND** fsck proceeds to subsequent phases

#### Scenario: All size-mismatched rows are still detected when batching

- **WHEN** the cache contains a known set of size-mismatched CDC `nar_file` rows
  distributed across multiple internal batches
- **THEN** every size-mismatched row appears in `narFilesWithSizeMismatch`
- **AND** no correctly-sized CDC row is falsely flagged

#### Scenario: SQLite and MySQL paths remain correct

- **WHEN** `ncps fsck` runs the size-mismatch check against SQLite or MySQL
- **THEN** the batched implementation returns the same result set as the previous
  unbatched implementation for caches small enough that both would succeed

### Requirement: Fsck chunk-walk MUST scale beyond database driver parameter limits

The implementation that resolves the chunk rows linked to a single `nar_file` (currently `chunksForNarFile`) SHALL NOT issue any single SQL statement whose bound-parameter
count grows unboundedly with the number of chunks belonging to that NAR. In particular,
it MUST NOT produce an `IN ($1...$M)` predicate on chunk IDs (whether emitted directly
or via an ORM eager-load) where M can exceed 65535.

The behavioral contract is unchanged: chunks are returned in `chunk_index` order, and
the returned slice length equals the link count.

#### Scenario: Resolving chunks for a NAR with > 65535 chunks succeeds on PostgreSQL

- **WHEN** `chunksForNarFile` is invoked for a `nar_file` whose `nar_file_chunks` row
  count exceeds the PostgreSQL extended-protocol parameter limit (65535)
- **THEN** the call returns without `extended protocol limited to 65535 parameters`
- **AND** the returned chunk slice contains every linked chunk in `chunk_index` order

### Requirement: Fsck MUST NOT introduce other unbounded IN-clause queries

Any future or existing fsck query that materializes a list of IDs (or composite keys) and then performs a secondary `In(...)` / `IN (...)` lookup keyed on that list SHALL
use the same bounded-batch helper used by the CDC size-mismatch path, or an
equivalent streaming approach. New code in `pkg/ncps/fsck.go` MUST NOT regress to
unbounded `With*` eager-loads or unbounded `In(...)` predicates on collections that
can grow with cache size or with the size of any single NAR's chunk list.

#### Scenario: Code review surfaces unbounded eager-loads

- **WHEN** a new fsck phase is added that walks a table whose row count grows with
  cache size and joins to a related table
- **THEN** the implementation uses the shared bounded-batch helper (or documents why
  the join cardinality is bounded by an unrelated cap)
- **AND** does not call `.All(ctx)` on an unbounded parent query followed by an
  Ent `With*(...)` eager-load on the same query

### Requirement: fsck MUST repair an unlinked narinfo with a backing nar_file instead of deleting it

When `fsck --repair` finds a narinfo with no `narinfo_nar_files` link, it previously deleted the narinfo as an orphan. Because a known race leaves valid, reachable narinfos unlinked from an *existing* nar_file, that deletion destroys live metadata and orphans the NAR. The system SHALL, before deleting such a narinfo, attempt to recreate the missing link: it SHALL parse the narinfo's URL, look up a `nar_file` matching that URL's hash and query (compression-agnostic), and — when one exists — create the `narinfo_nar_files` link and preserve the narinfo. The system SHALL delete the narinfo only when no backing `nar_file` exists for its URL anywhere in the database.

#### Scenario: Unlinked narinfo with a present backing nar_file is repaired

- **GIVEN** a narinfo with URL `nar/<H>.nar` and no `narinfo_nar_files` link
- **AND** a `nar_file` with hash `<H>` present in the database
- **WHEN** `fsck` runs the repair phase (not `--dry-run`)
- **THEN** the narinfo SHALL still exist after the run
- **AND** a `narinfo_nar_files` link SHALL now connect that narinfo and that `nar_file`

#### Scenario: Unlinked narinfo with no backing nar_file is still deleted

- **GIVEN** a narinfo with URL `nar/<H>.nar` and no `narinfo_nar_files` link
- **AND** NO `nar_file` matching `<H>` anywhere in the database
- **WHEN** `fsck` runs the repair phase (not `--dry-run`)
- **THEN** the narinfo SHALL be deleted (genuine orphans are reclaimed)

### Requirement: fsck MUST normalize a recoverable inconsistent chunked NAR immediately

When fsck `--repair` finds a chunked `nar_file` whose narinfo references it with a valid NarHash but advertises a non-Compression:none URL, it SHALL relink (if needed) and normalize that narinfo's URL to the Compression:none form. This operation touches no chunks and SHALL be performed regardless of CDC state. If the row carried a residue flag, fsck SHALL clear it.

#### Scenario: Recoverable inconsistent chunked NAR is normalized, not purged

- **GIVEN** a chunked `nar_file` for hash `H` whose narinfo has a valid NarHash but URL `nar/<H>.nar.xz`
- **WHEN** `fsck --repair` runs
- **THEN** the narinfo SHALL be normalized to URL `nar/<H>.nar` (Compression none)
- **AND** the `nar_file` SHALL remain chunked (not purged)

### Requirement: fsck MUST mark, not immediately purge, an un-de-chunkable chunked NAR

When fsck `--repair` finds a chunked `nar_file` with no narinfo carrying a resolvable NarHash (un-de-chunkable), it SHALL NOT purge it on first detection. Instead it SHALL record a persistent flag (`dechunk_residue_flagged_at`) if not already set. fsck SHALL purge the row only on a later run, once the flag has aged past a configurable grace window (default ~24h) AND the row is still un-de-chunkable at that later run. If the row becomes recoverable or de-chunked before the grace window elapses, fsck SHALL clear the flag and SHALL NOT purge it. This two-run, grace-windowed reclamation prevents purging transient states (a NAR mid-chunking, a narinfo not yet written) and protects legitimately chunked NARs. On a non-repair run (e.g. `--dry-run` or a monitoring check), fsck SHALL NOT report an un-de-chunkable chunked NAR as an active consistency issue unless it has already been flagged AND its grace window has elapsed; a row still within its grace window (or not yet flagged) SHALL NOT be counted as an issue, to avoid false positives and alert fatigue.

#### Scenario: First detection flags but does not purge

- **GIVEN** a chunked `nar_file` for hash `H` with no resolvable NarHash and no residue flag
- **WHEN** `fsck --repair` runs
- **THEN** `H`'s `dechunk_residue_flagged_at` SHALL be set
- **AND** `H` SHALL NOT be purged

#### Scenario: Aged, still-un-de-chunkable row is purged on a later run

- **GIVEN** a chunked `nar_file` for hash `H` with `dechunk_residue_flagged_at` older than the grace window
- **AND** `H` is still un-de-chunkable (no resolvable NarHash)
- **WHEN** `fsck --repair` runs
- **THEN** `H` SHALL be purged

#### Scenario: A row that became recoverable is unflagged, never purged

- **GIVEN** a chunked `nar_file` for hash `H` previously flagged
- **AND** `H` now has a narinfo with a resolvable NarHash
- **WHEN** `fsck --repair` runs
- **THEN** fsck SHALL clear `H`'s residue flag
- **AND** SHALL NOT purge `H`

### Requirement: fsck MUST NOT flag or purge a NAR that is actively being chunked

A chunked `nar_file` whose `chunking_started_at` indicates it is currently being written (within the chunking lock TTL) is not residue — it is a NAR mid-chunking. fsck SHALL NOT set the residue flag on, nor purge, such a row; it SHALL leave it untouched for the in-flight chunking to complete.

#### Scenario: A row with a recent chunking_started_at is left untouched

- **GIVEN** a chunked `nar_file` for hash `H` with `chunking_started_at` within the chunking lock TTL
- **WHEN** `fsck --repair` runs
- **THEN** fsck SHALL NOT set `H`'s `dechunk_residue_flagged_at`
- **AND** SHALL NOT purge `H`

### Requirement: Fsck MUST enable CDC mode when chunk residue exists, even after CDC and drain are fully disabled

`ncps fsck` SHALL determine `cdcMode` from any of the following signals, in order, treating the first match as sufficient:

1. The DB config key `cdc_enabled` equals `"true"`.
2. At least one `nar_file` has `total_chunks > 0`.
3. The `chunks` table is non-empty (at least one chunk row exists).

Signal 3 is the new residue signal. Orphaned chunks are, by definition, referenced by no `nar_file`, so signals 1 and 2 both go false once CDC is disabled and every NAR has been de-chunked — leaving residue undetectable without signal 3. The residue check SHALL be a single indexed existence query (`Chunk.Query().Exist(ctx)`), not a full scan.

When `cdcMode` is enabled, `ncps fsck` SHALL initialize the chunk store via the existing storage-backend configuration (independent of any CDC config keys) and run the established orphaned-chunk detection and, under `--repair`, reclamation.

When `cdcMode` is enabled **solely** by signal 3 (signals 1 and 2 both false), `ncps fsck` SHALL emit a distinct informational log indicating CDC mode was enabled because chunk residue was detected, so operators understand why a post-drain `fsck` is performing chunk work. This log SHALL be distinguishable from the existing fallback warning emitted when signal 2 triggers without signal 1.

#### Scenario: Post-drain orphaned chunks are detected and reclaimed

- **WHEN** `cdc_enabled` is absent from DB config, no `nar_file` has `total_chunks > 0`, the `chunks` table contains orphaned chunk rows, and `ncps fsck --repair` runs
- **THEN** `cdcMode` is enabled via the residue signal, the chunk store is initialized
- **AND** the orphaned chunk DB rows and their backing storage files are reclaimed
- **AND** a distinct "CDC mode enabled: chunk residue detected" informational log is emitted

#### Scenario: Post-drain dry-run reports residue without deleting

- **WHEN** the same post-drain residue exists and `ncps fsck` runs without `--repair`
- **THEN** `cdcMode` is enabled via the residue signal and the orphaned chunks are reported in the summary
- **AND** no chunk DB rows or storage files are deleted

#### Scenario: A cache that never used CDC stays in non-CDC mode

- **WHEN** `cdc_enabled` is absent, no `nar_file` has `total_chunks > 0`, and the `chunks` table is empty
- **THEN** `cdcMode` remains false, the chunk store is not initialized, and `fsck` performs no chunk checks

#### Scenario: Active CDC takes precedence over the residue signal

- **WHEN** `cdc_enabled` equals `"true"` and chunk rows also exist
- **THEN** `cdcMode` is enabled via signal 1 and the residue-detection log is NOT emitted

