## ADDED Requirements

### Requirement: De-chunk MUST content-verify the reconstructed NAR before committing

The de-chunk pass SHALL commit a NAR to whole-file storage only after it reconstructs the NAR from its chunks, computes the SHA-256 of the reconstructed bytes, and confirms that digest equals the resolved NarHash. The verification hash is the uncompressed NAR content hash, which is identical across every narinfo referencing the NAR regardless of the compression that narinfo's URL advertises; resolving it from any referencing narinfo (by NAR hash) therefore does NOT weaken verification. On a digest mismatch — or any reconstruction failure — the pass SHALL NOT write the whole file and SHALL NOT flip the record; it SHALL purge the chunked `nar_file` instead. The pass SHALL NEVER persist a de-chunked NAR it did not content-verify.

#### Scenario: Reconstructed-hash mismatch purges, never commits

- **GIVEN** a chunked `nar_file` for hash `H` and a resolved NarHash
- **WHEN** the reconstructed NAR's SHA-256 does NOT equal the resolved NarHash
- **THEN** the pass SHALL NOT write a whole file for `H`
- **AND** SHALL NOT flip `H` to `total_chunks = 0`
- **AND** SHALL purge the chunked `nar_file` for `H`

#### Scenario: Reconstructed-hash match commits

- **GIVEN** a chunked `nar_file` for hash `H` and a resolved NarHash
- **WHEN** the reconstructed NAR's SHA-256 equals the resolved NarHash
- **THEN** the pass SHALL write the whole file and flip `H` to `total_chunks = 0`

### Requirement: The de-chunk pass MUST always drive the chunked count to zero

A full `migrate-chunks-to-nar` pass over all chunked `nar_file` rows SHALL leave no row with `total_chunks > 0`. For every chunked NAR the pass SHALL either de-chunk it to whole-file storage or purge it; it SHALL NOT leave a NAR chunked because it could not resolve a verification hash or could not reconstruct the NAR.

#### Scenario: NarHash is resolved by NAR hash from any referencing narinfo

- **GIVEN** a chunked `nar_file` for hash `H` with no join link
- **AND** the only narinfo carrying a `nar_hash` for `H` advertises a different-compression URL (e.g. `nar/<H>.nar.xz`), not the bare `nar/<H>.nar`
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL resolve the verification NarHash from that narinfo (matched by NAR hash, not by exact URL)
- **AND** SHALL de-chunk `H` to whole-file storage

#### Scenario: Un-verifiable NAR is purged, not skipped

- **GIVEN** a chunked `nar_file` for hash `H` with no narinfo carrying a resolvable `nar_hash`
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL purge the chunked `nar_file` (removing its chunk links so a later request re-pulls from upstream)
- **AND** SHALL NOT leave `H` chunked
- **AND** SHALL NOT count `H` as a hard failure that aborts the run

#### Scenario: Hard reconstruction failure is purged, not failed-and-left

- **GIVEN** a chunked `nar_file` for hash `H` whose reconstruction fails (corrupt or missing chunks)
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL purge the chunked `nar_file`
- **AND** SHALL NOT leave `H` chunked

### Requirement: De-chunking MUST normalize the narinfo URL to none

When the de-chunk pass converts a NAR to whole-file (`Compression:none`) storage, it SHALL update every narinfo referencing that NAR to advertise the Compression:none URL (`nar/<H>.nar`, FileHash null, FileSize null), so the persisted narinfo is consistent with the whole-file storage and does not depend on serve-time chunk-based normalization.

#### Scenario: A de-chunked NAR's narinfo advertises none

- **GIVEN** a chunked NAR whose narinfo advertises `nar/<H>.nar.xz`
- **WHEN** the de-chunk pass de-chunks it to `none/whole`
- **THEN** the narinfo SHALL be updated to URL `nar/<H>.nar` and Compression none
- **AND** a subsequent serve of that narinfo SHALL NOT 404 the NAR
