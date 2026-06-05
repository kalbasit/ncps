## ADDED Requirements

### Requirement: De-chunk MUST resolve the verification NarHash via the narinfo URL when no join link exists

When de-chunking a NAR, the system resolves the expected NarHash from the linked narinfo in order to content-verify the reconstruction (verified-or-nothing). When the `narinfo_nar_files` join link is absent — a known race leaves CDC-chunked `nar_file` rows unlinked — the system SHALL fall back to resolving the narinfo by the NAR's `Compression:none` URL (`nar/<hash>.nar`) instead of treating the NAR as un-verifiable and skipping it. Only when no narinfo references the NAR by either the join link or its URL SHALL the de-chunk be skipped for want of a verification hash.

#### Scenario: Unlinked chunked NAR is de-chunked via the URL-resolved NarHash

- **GIVEN** a `nar_file` for hash `H` with `total_chunks > 0` and intact chunk links
- **AND** a narinfo with URL `nar/<H>.nar` carrying a recorded NarHash
- **AND** NO `narinfo_nar_files` link between that narinfo and the `nar_file`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL resolve the verification NarHash from the narinfo found by URL
- **AND** SHALL reconstruct the whole NAR from its chunks
- **AND** SHALL content-verify the reconstruction against that NarHash
- **AND** SHALL flip the record to whole-file (`total_chunks = 0`)

#### Scenario: NAR with neither a link nor a URL-matched narinfo is skipped

- **GIVEN** a chunked `nar_file` for hash `H` with no `narinfo_nar_files` link
- **AND** no narinfo whose URL is `nar/<H>.nar`
- **WHEN** `MigrateChunksToNar` is invoked for `H`
- **THEN** the system SHALL skip de-chunking (no NarHash to verify against)
- **AND** SHALL NOT delete or truncate the NAR
