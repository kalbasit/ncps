## ADDED Requirements

### Requirement: Serve a zstd request by recompressing uncompressed bytes

The system SHALL serve a `Compression: zstd` NAR request whose NAR is present only in an uncompressed representation (CDC chunks or an uncompressed whole file) by reassembling the uncompressed bytes and recompressing them to zstd on the fly. It MUST NOT return 404 for such a request when the uncompressed bytes are available locally, and the served stream MUST be labeled `zstd`.

#### Scenario: zstd request served from uncompressed CDC chunks

- **WHEN** a NAR is stored as uncompressed CDC chunks (no whole file in the store) and a client requests `/nar/<hash>.nar.zst`
- **THEN** the system reassembles the chunks, recompresses them to zstd, and streams a `zstd`-labeled response whose bytes decompress to the original NAR — without returning 404 or contacting an upstream

#### Scenario: uncompressed (none) request from chunks is unchanged

- **WHEN** a client requests `/nar/<hash>.nar` (Compression: none) for a chunked NAR
- **THEN** the system serves the reassembled uncompressed bytes as before (no recompression)

#### Scenario: a request for a non-producible compression still falls back

- **WHEN** a client requests a compression the system has no compressor for (e.g. `xz`) and the NAR is available only as uncompressed chunks
- **THEN** the system does not fabricate the stream; it returns not-found so the request can fall back (upstream / repair), rather than serving mislabeled bytes
