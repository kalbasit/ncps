## MODIFIED Requirements

### Requirement: Reconstruct a whole NAR from its chunks, verified against the recorded hash

The system SHALL provide a `migrate-chunks-to-nar` operation that, for a chunked `nar_file`, reconstructs the whole NAR by concatenating its chunks in `chunk_index` order, verifies the reconstructed bytes against the recorded `NarHash` (and `NarSize`), writes the whole NAR to the NAR store, and flips the `nar_file` record to the whole-file representation. Verification is mandatory: if the reconstructed hash or size does not match the recorded values, or if a referenced chunk is absent from the chunk store, the operation SHALL purge the corrupt entry — delete all `nar_file_chunks` links for the affected `nar_file`, delete any chunk objects that become unreferenced as a result, and delete the `nar_file` record — so the NAR can be re-fetched from upstream on the next `GetNar` request. The purge SHALL NOT write any whole-file bytes to the NAR store for the affected hash.

#### Scenario: Chunked NAR is reconstructed, verified, and stored whole

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0` and all chunks present
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL stream the chunks in order, compute the NAR hash, and confirm it equals the recorded `NarHash`
- **AND** SHALL write the whole NAR to the NAR store
- **AND** the resulting `nar_file` record SHALL represent a whole-file NAR (`total_chunks = 0`, no chunk links)
- **AND** a subsequent `GetNar` for `H` SHALL serve the whole file

#### Scenario: Hash mismatch purges the NAR and evicts it for re-fetch

- **GIVEN** a chunked `nar_file` for hash `H` whose reconstructed bytes do not match `NarHash`
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL delete all `nar_file_chunks` links for `H`
- **AND** SHALL delete any chunk objects that are now unreferenced (not linked to any other `nar_file`)
- **AND** SHALL delete the `nar_file` record for `H`
- **AND** SHALL NOT write whole-file bytes to the NAR store for `H`
- **AND** a subsequent `GetNar` for `H` SHALL trigger a fresh upstream fetch

#### Scenario: Missing chunk purges the NAR and evicts it for re-fetch

- **GIVEN** a chunked `nar_file` for hash `H` with at least one referenced chunk absent from the chunk store
- **WHEN** `migrate-chunks-to-nar` processes `H`
- **THEN** the system SHALL delete all `nar_file_chunks` links for `H`
- **AND** SHALL delete any chunk objects that are now unreferenced
- **AND** SHALL delete the `nar_file` record for `H`
- **AND** a subsequent `GetNar` for `H` SHALL trigger a fresh upstream fetch

#### Scenario: Purge retains narinfo record

- **GIVEN** a chunked `nar_file` for hash `H` with a linked `narinfo` record
- **WHEN** `migrate-chunks-to-nar` purges `H` due to hash mismatch or missing chunk
- **THEN** the `narinfo` record for `H` SHALL remain in the database
- **AND** a subsequent `GetNarInfo` for `H` SHALL return the narinfo
- **AND** the subsequent `GetNar` for `H` SHALL fetch from upstream and re-store the NAR correctly

#### Scenario: Purge is dedup-safe for shared chunks

- **GIVEN** chunk `C` is referenced by both hash `H1` (being purged) and hash `H2` (still chunked)
- **WHEN** `migrate-chunks-to-nar` purges `H1`
- **THEN** chunk `C` SHALL remain in the chunk store because `H2` still references it

### Requirement: A per-NAR failure MUST NOT abort the batch

When processing many NARs, a failure on one NAR (hash mismatch, missing chunk, I/O error) SHALL be recorded and SHALL NOT prevent the remaining NARs from being processed. Hash mismatch and missing-chunk failures are unrecoverable and result in a purge; transient errors (I/O errors, lock failures, query errors) are counted as failures. The command SHALL exit non-zero only when at least one transient failure occurred. The command SHALL exit 0 when all NARs were either successfully migrated or purged. The command SHALL report migrated, purged, skipped, and failed counts in the final summary log line.

#### Scenario: Hash-mismatch NAR is purged; batch continues; exit 0

- **GIVEN** a batch where hash `H_bad` fails verification and other NARs are valid
- **WHEN** `migrate-chunks-to-nar` runs over the batch
- **THEN** every valid NAR SHALL be migrated
- **AND** `H_bad` SHALL be purged (nar_file + orphaned chunks deleted)
- **AND** the command SHALL exit 0
- **AND** the summary log SHALL report `purged=1`

#### Scenario: Transient I/O error still causes non-zero exit

- **GIVEN** a batch where hash `H_io` fails due to a transient I/O error (not a hash mismatch)
- **WHEN** `migrate-chunks-to-nar` runs over the batch
- **THEN** `H_io` SHALL be counted as failed (not purged)
- **AND** the command SHALL exit non-zero
- **AND** the summary log SHALL report `failed=1`
