## ADDED Requirements

### Requirement: Transport-level zstd is negotiated only for uncompressed NARs

The system SHALL send `Accept-Encoding: zstd` on an upstream NAR fetch only when
the NAR's resolved compression is `none`, and SHALL NOT send it for a NAR whose
narinfo declares any other compression. Transport-level compression of content
that is already compressed yields effectively no bandwidth saving, and requesting
it invites a compressing proxy in front of the upstream to wrap the body — which
the system then strips, leaving it holding bytes that do not match the
compression the narinfo declares.

The system SHALL continue to transparently decompress a response that carries
`Content-Encoding: zstd`, since that remains the correct handling of a transfer
encoding whether or not it was solicited.

This requirement depends on the narinfo `Compression:` header being authoritative
when the URL carries no compression extension: the resolved compression is only
trustworthy because of that.

#### Scenario: Uncompressed NAR still negotiates transport zstd

- **WHEN** the system fetches a NAR whose resolved compression is `none`
- **THEN** the request SHALL carry `Accept-Encoding: zstd`
- **AND** a `Content-Encoding: zstd` response SHALL be transparently decompressed

#### Scenario: Compressed NAR is fetched without transport negotiation

- **WHEN** the system fetches a NAR whose resolved compression is `zstd`, `xz`, or any other non-`none` compression
- **THEN** the request SHALL NOT carry `Accept-Encoding: zstd`

#### Scenario: A compressing proxy no longer corrupts a compressed NAR

- **GIVEN** an upstream serving an uncompressed body for a narinfo that declares `Compression: zstd`, behind a proxy that applies zstd only when the client advertises it
- **WHEN** the system fetches that NAR
- **THEN** the proxy SHALL NOT wrap the body, because the system did not advertise the encoding
- **AND** the bytes the system stores and serves SHALL decode under the compression the narinfo declares

#### Scenario: An unsolicited Content-Encoding is still handled

- **WHEN** an upstream returns `Content-Encoding: zstd` on a response the system did not negotiate it for
- **THEN** the system SHALL still transparently decompress the body rather than treating the encoding as content
