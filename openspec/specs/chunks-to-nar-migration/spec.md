# chunks-to-nar-migration Specification

## Purpose

Defines the `migrate-chunks-to-nar` operation — the reverse of `migrate-nar-to-chunks` — which reconstructs CDC-chunked NARs back into verified whole files so a deployment can safely exit CDC, with idempotent/resumable execution and dedup-safe chunk reclamation.

## Requirements
### Requirement: Reconstruct a whole NAR from its chunks, verified against the recorded hash

The system SHALL provide a `migrate-chunks-to-nar` operation that, for a chunked `nar_file`, reconstructs the whole NAR by concatenating its chunks in `chunk_index` order, verifies the reconstructed bytes against the recorded `NarHash` (and `NarSize`), writes the whole NAR to the NAR store, and flips the `nar_file` record to the whole-file representation. Verification is mandatory: if the reconstructed hash or size does not match the recorded values, the operation SHALL fail for that NAR **without** writing a bad whole-file, deleting chunks, or mutating the record.

#### Scenario: Chunked NAR is reconstructed, verified, and stored whole

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0` and all chunks present
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL stream the chunks in order, compute the NAR hash, and confirm it equals the recorded `NarHash`
- **AND** SHALL write the whole NAR to the NAR store
- **AND** the resulting `nar_file` record SHALL represent a whole-file NAR (`total_chunks = 0`, no chunk links)
- **AND** a subsequent `GetNar` for `H` SHALL serve the whole file

#### Scenario: Hash mismatch aborts the NAR without destructive effects

- **GIVEN** a chunked `nar_file` for hash `H` whose reconstructed bytes do not match `NarHash`
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL report an error for `H`
- **AND** SHALL NOT delete any chunk of `H`
- **AND** SHALL NOT flip the `nar_file` record to whole-file
- **AND** SHALL NOT leave a verified-as-good whole file in the store for `H`

#### Scenario: Missing chunk aborts the NAR without destructive effects

- **GIVEN** a chunked `nar_file` for hash `H` with at least one referenced chunk absent from the chunk store
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL report an error for `H` and leave its records and remaining chunks untouched

### Requirement: Migration MUST be idempotent and resumable

The operation SHALL be safe to re-run and safe to resume after interruption. A NAR already in the whole-file representation SHALL be skipped. An interruption SHALL NOT leave a half-written whole file presented as complete, nor delete chunks before the whole file is durably stored and the record flipped.

#### Scenario: Already-whole NAR is skipped

- **GIVEN** a `nar_file` for hash `H` that is already whole-file (`total_chunks = 0`)
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL treat `H` as already migrated and make no changes

#### Scenario: Re-run after interruption completes cleanly

- **GIVEN** a run was interrupted while migrating hash `H` (e.g. after writing the whole file but before flipping the record, or before reclaiming chunks)
- **WHEN** `migrate-chunks-to-nar` is run again
- **THEN** it SHALL bring `H` to a consistent whole-file state without producing a corrupt or short whole file
- **AND** SHALL NOT require manual cleanup of partial artifacts

### Requirement: Chunk reclamation MUST be deferred by default and always dedup-safe

The system SHALL NOT delete a NAR's chunks as part of de-chunking by default: a concurrent serve that began streaming from chunks before the record was flipped may still be reading those chunk files, and deleting them mid-stream would truncate that transfer. By default the now-unreferenced chunks SHALL be left for the regular garbage collector to reclaim. The operation SHALL provide an explicit opt-in (`--force-reclaim`) for callers that assert traffic is drained (e.g. a maintenance-window run), which reclaims unreferenced chunks immediately. In either path a chunk SHALL be deleted only when no `nar_file` references it (no remaining `nar_file_chunks` links); a chunk shared with another still-chunked NAR SHALL NEVER be deleted.

#### Scenario: Default run does not delete chunks

- **GIVEN** a chunked NAR `H` whose chunks are referenced only by `H`
- **WHEN** `H` is migrated to whole-file without `--force-reclaim`
- **THEN** the `nar_file` SHALL be flipped to whole-file (links removed, `total_chunks = 0`)
- **AND** the chunk objects SHALL remain in the store (left for the GC), so an in-flight chunk-serve is not truncated

#### Scenario: Force-reclaim deletes a now-orphaned chunk

- **GIVEN** chunk `C` is referenced only by hash `H` (being migrated)
- **WHEN** `H` is migrated to whole-file with `--force-reclaim`
- **THEN** chunk `C` SHALL be deleted from the chunk store as it is now unreferenced

#### Scenario: Shared chunk is retained even with force-reclaim

- **GIVEN** chunk `C` is referenced by both hash `H1` (migrated with `--force-reclaim`) and hash `H2` (still chunked)
- **WHEN** `H1` is migrated to whole-file and its chunk links removed
- **THEN** chunk `C` SHALL remain in the chunk store because `H2` still references it

### Requirement: A dry-run mode MUST make no changes

The operation SHALL support a `--dry-run` flag that reports what would be migrated and reclaimed without writing whole files, mutating records, or deleting chunks.

#### Scenario: Dry-run reports without mutating

- **GIVEN** chunked NARs eligible for migration
- **WHEN** `migrate-chunks-to-nar --dry-run` is run
- **THEN** the system SHALL report the NARs it would migrate and chunks it would reclaim
- **AND** SHALL NOT write to the NAR store, mutate `nar_file` records, or delete chunks

### Requirement: A per-NAR failure MUST NOT abort the batch

When processing many NARs, a failure on one NAR (hash mismatch, missing chunk, I/O error) SHALL be recorded and SHALL NOT prevent the remaining NARs from being processed. The command SHALL exit non-zero if any NAR failed, and report the failed hashes.

#### Scenario: One bad NAR does not stop the others

- **GIVEN** a batch where hash `H_bad` fails verification and other NARs are valid
- **WHEN** `migrate-chunks-to-nar` runs over the batch
- **THEN** every valid NAR SHALL be migrated
- **AND** `H_bad` SHALL be reported as failed
- **AND** the command SHALL exit non-zero

