## ADDED Requirements

### Requirement: CDC narinfo URL normalization MUST NOT be predicted at store time

When a narinfo is pulled from upstream, the system SHALL persist the narinfo URL and compression that reflect the NAR's actual stored representation. The system SHALL NOT rewrite the persisted narinfo URL to `nar/<hash>.nar` (Compression none) at store time merely because CDC is enabled — because the asynchronous chunking that would make `none` true may not have completed, or may never complete. Store-time normalization to `none` SHALL be performed only when the upstream narinfo itself advertises `Compression: none` (the genuinely-uncompressed / Harmonia case, stored as zstd and served as none with transparent encoding).

Presentation of `url=none` for a chunked CDC NAR is provided exclusively at serve time by `maybeCDCNormalizeNarInfoURL`, gated on `HasNarInChunks` returning true. Consequently a CDC narinfo whose NAR is not yet chunked is served with its truthful upstream compression, and one whose NAR is chunked is served as `none`.

#### Scenario: Eager-CDC pull of an xz upstream narinfo persists the truthful xz URL

- **GIVEN** CDC is enabled (eager, lazy chunking disabled)
- **AND** an upstream narinfo with `Compression: xz` and URL `nar/<H>.nar.xz`
- **AND** the NAR has not (yet) been chunked
- **WHEN** the narinfo is pulled and persisted
- **THEN** the persisted narinfo URL SHALL be `nar/<H>.nar.xz`
- **AND** the persisted compression SHALL be `xz`
- **AND** the persisted narinfo SHALL NOT advertise a `none` URL

#### Scenario: Chunked CDC NAR is still served as none at serve time

- **GIVEN** a narinfo persisted with `Compression: xz`
- **AND** its NAR has been chunked (`HasNarInChunks` returns true)
- **WHEN** the narinfo is served via `GetNarInfo`
- **THEN** the served narinfo SHALL present `Compression: none` and URL `nar/<H>.nar`
- **AND** the persisted DB record SHALL remain unchanged (presentation is serve-time only)

#### Scenario: Genuinely-uncompressed upstream is still normalized to none

- **GIVEN** an upstream narinfo with `Compression: none`
- **WHEN** the narinfo is pulled and persisted
- **THEN** the persisted narinfo SHALL have `Compression: none` and URL `nar/<H>.nar`
