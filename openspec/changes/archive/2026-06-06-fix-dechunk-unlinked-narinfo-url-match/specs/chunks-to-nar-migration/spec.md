## MODIFIED Requirements

### Requirement: De-chunk MUST resolve the verification NarHash via the narinfo URL when no join link exists

When de-chunking a NAR, the system resolves the expected NarHash from the linked narinfo in order to content-verify the reconstruction (verified-or-nothing). When the `narinfo_nar_files` join link is absent — a known race leaves CDC-chunked `nar_file` rows unlinked — the system SHALL fall back to resolving the narinfo by the NAR's hash, matching **hash-aware** rather than by raw URL prefix: a candidate narinfo references the NAR when its URL, parsed and normalized (narinfo-hash prefix stripped), has the same NAR hash. This SHALL cover both canonical URLs (`nar/<hash>.nar[.<ext>]`) and nix-serve-style prefixed URLs (`nar/<narinfoHash>-<hash>.nar[.<ext>]`). Only when no narinfo references the NAR by either the join link or a hash-matched URL SHALL the de-chunk be skipped for want of a verification hash.

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

#### Scenario: NAR with neither a link nor a hash-matched narinfo is skipped

- **GIVEN** a chunked `nar_file` for hash `H` with no `narinfo_nar_files` link
- **AND** no narinfo whose URL normalizes to hash `H`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL skip de-chunking (no NarHash to verify against)
- **AND** SHALL NOT delete or truncate the NAR

### Requirement: De-chunking MUST normalize the narinfo URL to none

When the de-chunk pass converts a NAR to whole-file (`Compression:none`) storage, it SHALL update every narinfo referencing that NAR to advertise the Compression:none URL (`nar/<H>.nar`, FileHash null, FileSize null), so the persisted narinfo is consistent with the whole-file storage and does not depend on serve-time chunk-based normalization. "Every narinfo referencing that NAR" SHALL be identified by the `narinfo_nar_files` join link OR, for unlinked rows, by a **hash-aware** URL match (the candidate URL parsed and normalized to the same NAR hash) — covering both canonical and nix-serve-style prefixed URLs — not by a raw URL prefix that would miss prefixed URLs.

#### Scenario: A de-chunked NAR's narinfo advertises none

- **GIVEN** a chunked NAR whose narinfo advertises `nar/<H>.nar.xz`
- **WHEN** the de-chunk pass de-chunks it to `none/whole`
- **THEN** the narinfo SHALL be updated to URL `nar/<H>.nar` and Compression none
- **AND** a subsequent serve of that narinfo SHALL NOT 404 the NAR

#### Scenario: An unlinked, prefixed-URL narinfo is normalized on de-chunk

- **GIVEN** a de-chunked NAR for hash `H`
- **AND** a narinfo with a prefixed URL `nar/<narinfoHash>-<H>.nar.xz` and NO join link to the `nar_file`
- **WHEN** the de-chunk pass normalizes referencing narinfos
- **THEN** the prefixed-URL narinfo SHALL be updated to URL `nar/<H>.nar` and Compression none
- **AND** a subsequent serve of that narinfo SHALL NOT 404 the NAR
