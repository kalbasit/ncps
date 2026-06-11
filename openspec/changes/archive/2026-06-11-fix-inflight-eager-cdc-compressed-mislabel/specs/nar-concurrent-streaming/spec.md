## ADDED Requirements

### Requirement: In-flight live-streaming MUST NOT serve a compressed request as a mislabeled uncompressed body

The system SHALL serve a NAR request that piggybacks on an in-flight upstream
download (the per-client live-streaming path, where the holder's temp file is
held only as the uncompressed/decompressed representation under eager CDC) in
the client's originally requested compression, or fall back with not-found. It
MUST NOT emit an HTTP 200 whose body is in a different compression than the
client requested. Specifically, when the holder's in-flight temp bytes are
uncompressed:

- a `Compression: none` request SHALL stream the uncompressed bytes (unchanged);
- a request for any non-matching compression the in-flight uncompressed holder
  cannot directly satisfy (e.g. `xz` or `zstd`) SHALL return
  `storage.ErrNotFound` so the client falls back to an upstream that has the
  original file, rather than streaming the uncompressed bytes mislabeled as the
  requested compression. Steady-state requests for such a NAR are unaffected:
  once it is fully chunked, a `zstd` request is served by recompression
  (`nar-serving-recompression`) and a `none` request by reassembly; only the
  brief in-flight window falls back.

The decision SHALL use the same fallback predicate
(`compressedRequestNeedsUpstreamFallback`) as the in-flight staging serve path,
so both paths share one rule. The requested compression captured at request
entry SHALL gate this branch; the holder temp file's compression MUST NOT
silently overwrite it on the served stream.

#### Scenario: xz request during the in-flight eager-CDC window falls back, not mislabeled

- **WHEN** eager CDC is enabled and a holder is mid pull-through for hash `H` (its temp file holds the decompressed, uncompressed NAR and chunking is in progress)
- **AND** a client requests `/nar/<H>.nar.xz`
- **THEN** the system SHALL return `storage.ErrNotFound` (HTTP 404) so the client falls back to an upstream that still has the `.nar.xz`
- **AND** the system SHALL NOT return an HTTP 200 whose body is the uncompressed NAR labeled `Compression: none`

#### Scenario: zstd request during the in-flight window falls back, not mislabeled

- **WHEN** a holder is mid pull-through for hash `H` with an uncompressed in-flight temp file
- **AND** a client requests `/nar/<H>.nar.zst`
- **THEN** the system SHALL return `storage.ErrNotFound` (HTTP 404) so the client falls back to an upstream that has the original file
- **AND** the system SHALL NOT serve the uncompressed bytes labeled `Compression: none`
- **AND** once `H` is fully chunked a later `/nar/<H>.nar.zst` request SHALL be served by recompression (`nar-serving-recompression`), unaffected by this in-flight rule

#### Scenario: none request during the in-flight window is unchanged

- **WHEN** a holder is mid pull-through for hash `H` with an uncompressed in-flight temp file
- **AND** a client requests `/nar/<H>.nar` (Compression: none)
- **THEN** the system SHALL stream the uncompressed bytes labeled `Compression: none` as before

#### Scenario: served stream reports the requested compression, not the holder temp compression

- **WHEN** `GetNar` serves a compressed request from an in-flight holder whose temp file is uncompressed
- **THEN** the returned NAR URL's compression SHALL equal the client's requested compression (for a producible compression) or the call SHALL surface `storage.ErrNotFound`
- **AND** the returned compression SHALL NOT be silently set to the holder temp file's compression for a compressed request
