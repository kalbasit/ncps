## ADDED Requirements

### Requirement: Compressed upstream narinfos missing FileSize/FileHash MUST be tolerated and self-completed

An upstream narinfo that declares a non-`none` `Compression` (e.g. `zstd`, `xz`) but omits
the optional `FileSize` and/or `FileHash` fields SHALL NOT be rejected. The system SHALL
accept such a narinfo, fetch its NAR, and serve it. `FileSize`/`FileHash` are optional in
the narinfo format, so their absence is not an error.

For a compressed NAR served under its original compression (i.e. not normalized to
`Compression: none` and not stored as CDC chunks), the system SHALL ensure the narinfo it
serves downstream carries a correct `FileSize` and `FileHash`. When upstream supplies them,
the system SHALL preserve the upstream values unchanged. When upstream omits either, the
system SHALL compute the missing value(s) itself from the compressed NAR bytes as they pass
through during the NAR fetch: `FileSize` SHALL be the byte length of the stored compressed
NAR, and `FileHash` SHALL be the SHA-256 digest of the stored compressed NAR (formatted as
a nix `sha256:<nixbase32>` hash). The computed values SHALL be backfilled into the persisted
narinfo so subsequent narinfo responses carry them.

The computation SHALL tap the existing NAR byte stream (no additional download or
full-file buffering) and SHALL NOT alter the NAR bytes, the `NarHash`, the `NarSize`, or
the `Compression` advertised to downstream clients.

#### Scenario: Compressed narinfo without FileSize/FileHash is accepted

- **GIVEN** an upstream narinfo with `Compression: zstd`, a valid `NarHash`/`NarSize`, and no `FileSize`/`FileHash`
- **WHEN** the narinfo is fetched from upstream
- **THEN** the fetch SHALL succeed rather than failing with `invalid narinfo: FileSize is missing for a compressed NAR`
- **AND** the request SHALL be served with HTTP 200 rather than 404

#### Scenario: ncps computes FileSize and FileHash from the fetched compressed NAR

- **GIVEN** a compressed (non-CDC, non-normalized) NAR whose upstream narinfo omitted `FileSize`/`FileHash`
- **WHEN** ncps fetches and stores the NAR
- **THEN** the served narinfo SHALL report a `FileSize` equal to the byte length of the stored compressed NAR
- **AND** the served narinfo SHALL report a `FileHash` equal to the SHA-256 digest of the stored compressed NAR, formatted as `sha256:<nixbase32>`
- **AND** the `NarHash`, `NarSize`, and `Compression` SHALL be unchanged from upstream

#### Scenario: Upstream-provided FileSize/FileHash are preserved, not recomputed

- **GIVEN** an upstream narinfo with `Compression: zstd` that already provides both `FileSize` and `FileHash`
- **WHEN** the narinfo is fetched and the NAR is served
- **THEN** the served narinfo SHALL carry the upstream `FileSize` and `FileHash` verbatim
- **AND** ncps SHALL NOT recompute them

#### Scenario: Uncompressed narinfos are unaffected

- **GIVEN** an upstream narinfo with `Compression: none` (or empty) and no `FileSize`/`FileHash`
- **WHEN** the narinfo is fetched
- **THEN** existing `Compression: none` handling SHALL apply unchanged
- **AND** no compressed-file hash SHALL be computed
