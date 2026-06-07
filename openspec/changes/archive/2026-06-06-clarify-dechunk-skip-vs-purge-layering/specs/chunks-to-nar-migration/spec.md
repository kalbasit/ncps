## MODIFIED Requirements

### Requirement: De-chunk MUST resolve the verification NarHash via the narinfo URL when no join link exists

When de-chunking a NAR, the system resolves the expected NarHash from the linked narinfo in order to content-verify the reconstruction (verified-or-nothing). When the `narinfo_nar_files` join link is absent — a known race leaves CDC-chunked `nar_file` rows unlinked — the system SHALL fall back to resolving the narinfo by the NAR's hash, matching **hash-aware** rather than by raw URL prefix: a candidate narinfo references the NAR when its URL, parsed and normalized (narinfo-hash prefix stripped), has the same NAR hash. This SHALL cover both canonical URLs (`nar/<hash>.nar[.<ext>]`) and nix-serve-style prefixed URLs (`nar/<narinfoHash>-<hash>.nar[.<ext>]`).

This requirement governs the **single `MigrateChunksToNar` operation**. When no narinfo references the NAR by either the join link or a hash-matched URL, that operation SHALL NOT de-chunk and SHALL NOT delete or truncate the NAR (verified-or-nothing); it SHALL signal the unverifiable condition (`ErrNoNarHashToVerify`) and leave the NAR chunked. Driving the chunked count to zero is the responsibility of the **batch pass**, which purges such NARs (see "The de-chunk pass MUST always drive the chunked count to zero"); the two are layers of one policy, not alternatives.

#### Scenario: Unlinked chunked NAR is de-chunked via the URL-resolved NarHash

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0` and intact chunk links
- **AND** a narinfo with URL `nar/<H>.nar` carrying a recorded NarHash
- **AND** NO `narinfo_nar_files` link between that narinfo and the `nar_file`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL resolve the verification NarHash from the narinfo found by URL
- **AND** SHALL reconstruct the whole NAR from its chunks
- **AND** SHALL content-verify the reconstruction against that NarHash
- **AND** SHALL flip the record to whole-file (`total_chunks = 0`)

#### Scenario: Unlinked narinfo with a nix-serve-style prefixed URL is matched hash-aware

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0`
- **AND** a narinfo with a prefixed URL `nar/<narinfoHash>-<H>.nar.xz` carrying a recorded NarHash
- **AND** NO `narinfo_nar_files` link between that narinfo and the `nar_file`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL recognize the narinfo as referencing `H` by its normalized embedded hash (not by a raw `nar/<H>.` prefix)
- **AND** SHALL resolve the verification NarHash from it and de-chunk `H`

#### Scenario: The single operation leaves an unverifiable NAR chunked (the pass purges it)

- **GIVEN** a chunked `nar_file` for hash `H` with no `narinfo_nar_files` link
- **AND** no narinfo whose URL normalizes to hash `H`
- **WHEN** the single `MigrateChunksToNar` operation is invoked for `H`
- **THEN** it SHALL NOT de-chunk `H` and SHALL NOT delete or truncate the NAR
- **AND** it SHALL signal the unverifiable condition (`ErrNoNarHashToVerify`), leaving `H` chunked
- **AND** the batch pass SHALL subsequently purge `H` to drive the chunked count to zero (per "The de-chunk pass MUST always drive the chunked count to zero")

### Requirement: The de-chunk pass MUST always drive the chunked count to zero

A full `migrate-chunks-to-nar` pass over all chunked `nar_file` rows SHALL leave no row with `total_chunks > 0`. For every chunked NAR the pass SHALL either de-chunk it to whole-file storage or purge it; it SHALL NOT leave a NAR chunked because it could not resolve a verification hash or could not reconstruct the NAR. This is the **pass-level** complement to the single-operation verified-or-nothing rule: when the single `MigrateChunksToNar` operation declines to de-chunk an unverifiable NAR (signalling `ErrNoNarHashToVerify`) or fails reconstruction, the pass SHALL purge that NAR (removing its chunk links so a later request re-fetches it from upstream) rather than leaving it chunked. An interruption (context cancellation) is NOT such a failure: the pass SHALL leave the NAR chunked for a later run rather than purge a possibly-healthy NAR.

#### Scenario: NarHash is resolved by NAR hash from any referencing narinfo

- **GIVEN** a chunked `nar_file` for hash `H` with no join link
- **AND** the only narinfo carrying a `nar_hash` for `H` advertises a different-compression URL (e.g. `nar/<H>.nar.xz`), not the bare `nar/<H>.nar`
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL resolve the verification NarHash from that narinfo (matched by NAR hash, not by exact URL)
- **AND** SHALL de-chunk `H` to whole-file storage

#### Scenario: Un-verifiable NAR is purged by the pass, not left chunked

- **GIVEN** a chunked `nar_file` for hash `H` with no narinfo carrying a resolvable `nar_hash`
- **WHEN** the de-chunk pass processes `H` (the single operation signals `ErrNoNarHashToVerify`)
- **THEN** the pass SHALL purge the chunked `nar_file` (removing its chunk links so a later request re-pulls from upstream)
- **AND** SHALL NOT leave `H` chunked
- **AND** SHALL NOT count `H` as a hard failure that aborts the run

#### Scenario: Hard reconstruction failure is purged, not failed-and-left

- **GIVEN** a chunked `nar_file` for hash `H` whose reconstruction fails (corrupt or missing chunks)
- **WHEN** the de-chunk pass processes `H`
- **THEN** it SHALL purge the chunked `nar_file`
- **AND** SHALL NOT leave `H` chunked

#### Scenario: An interrupted run leaves the NAR chunked rather than purging

- **GIVEN** a chunked `nar_file` for hash `H`
- **WHEN** the de-chunk pass is cancelled (context cancellation / deadline) while processing `H`
- **THEN** the pass SHALL leave `H` chunked for a later run
- **AND** SHALL NOT purge `H`
