## ADDED Requirements

### Requirement: The narinfo Compression header is authoritative when the URL carries no compression extension

The system SHALL resolve an upstream NAR's compression from the narinfo
`Compression:` header whenever the upstream `URL:` field carries no compression
extension, and SHALL treat that resolved compression as the single value used for
the narinfo it re-serves, for the `nar_file` row it records, and for the encoding
under which it stores the NAR. A URL that does carry an explicit compression
extension keeps that extension as the authoritative value, so upstreams which
encode compression in the URL (cache.nixos.org, cachix, Harmonia, nix-serve) are
unaffected.

This closes the narinfo↔nar_file compression desync produced by upstreams that
state compression only in the header — notably Attic, whose `URL:` is always a
bare `nar/<storePathHash>.nar` while `Compression:` is `zstd`.

#### Scenario: Bare .nar URL with a compressed narinfo resolves to the header compression

- **WHEN** an upstream narinfo has `URL: nar/<hash>.nar` (no compression extension) and `Compression: zstd`
- **THEN** the resolved compression SHALL be `zstd`, not `none`

#### Scenario: The narinfo and its nar_file row agree on compression

- **WHEN** a narinfo whose `URL:` carries no compression extension and whose `Compression:` is a compressed type is pulled and stored
- **THEN** the compression recorded on the linked `nar_file` row SHALL equal the compression advertised on the stored narinfo

#### Scenario: An already-compressed NAR is not re-compressed on ingest

- **WHEN** the resolved compression is a compressed type
- **THEN** the NAR SHALL be stored under that compression as received
- **AND** it SHALL NOT be re-compressed as `.nar.zst` on top of its existing compression

#### Scenario: Bare .nar URL with an uncompressed narinfo is unchanged

- **WHEN** an upstream narinfo has `URL: nar/<hash>.nar` and `Compression: none` (or no `Compression:` field)
- **THEN** the resolved compression SHALL be `none` and the NAR SHALL continue to be stored as the canonical `.nar.zst` encoding

#### Scenario: An explicit URL compression extension wins over the header

- **WHEN** an upstream narinfo has `URL: nar/<hash>.nar.xz` and a `Compression:` header naming a different compression
- **THEN** the resolved compression SHALL be the URL's `xz`, preserving today's behaviour

#### Scenario: The served narinfo URL carries the resolved compression extension

- **WHEN** the resolved compression is a compressed type and the upstream URL carried no extension
- **THEN** the narinfo ncps re-serves SHALL advertise a URL bearing that compression's extension, so a client fetching it receives bytes matching the advertised `Compression:`

#### Scenario: The original upstream path survives the resolution

- **WHEN** the compression was resolved from the narinfo header rather than from a URL extension
- **THEN** the upstream GET SHALL target the original extension-less upstream path verbatim, not ncps's re-serve URL
- **AND** that path SHALL be persisted so the NAR can be re-fetched from upstream after the local copy is evicted

## MODIFIED Requirements

### Requirement: Opaque (non hash-named) upstream NAR URLs MUST be tolerated

The system MUST proxy an upstream narinfo whose `URL:` field is not a conventional
hash-named `nar/<hash>.nar[.<compression>]` path rather than rejecting it. This
covers two opaque shapes:

1. **Non-hash filename that still ends in `.nar[.<compression>]`** — e.g. cachix's
   `nar/<uuidv4>.nar.zst`, or Attic's `nar/<storePathHash>.nar` (the stem before
   `.nar` is not a valid Nix hash in either case).
2. **No `.nar` token at all** — e.g. snix-castore's
   `nar/snix-castore/<base64-blob>?narsize=N`, served with `Compression: none`.

For any opaque URL the system SHALL derive its local storage key from the narinfo
`NarHash` (re-encoded as a bare nix32 digest), SHALL preserve the original opaque
upstream path **including its query string** verbatim for the upstream GET, and
SHALL re-serve the NAR to downstream clients under its own hash-named URL keyed off
the `NarHash`. For an opaque URL that carries no compression extension — whether it
ends in a bare `.nar` or has no `.nar` token at all — the compression SHALL be taken
from the narinfo `Compression:` header, defaulting to `none` when that header is
absent or already `none`. Conventional hash-named upstream URLs SHALL continue to be
handled exactly as before.

When an opaque upstream URL is encountered but the narinfo carries no usable
`NarHash`, the system SHALL surface a parse error rather than fabricate a storage
key. The strict parser used for ncps's own serve/storage keys SHALL remain
unchanged, so ncps continues to emit only hash-named URLs to clients.

#### Scenario: Opaque upstream URL is proxied successfully

- **WHEN** an upstream narinfo has an opaque `URL:` (e.g. `nar/<uuid>.nar.zst`) and a valid `NarHash`
- **THEN** the request SHALL succeed rather than failing with an invalid-nar-hash error
- **AND** the served narinfo `URL:` SHALL be ncps's own hash-named URL derived from the `NarHash`
- **AND** the NAR bytes SHALL be fetched from the original opaque upstream path

#### Scenario: snix-castore `.nar`-less upstream URL is proxied successfully

- **WHEN** an upstream narinfo has an opaque `URL:` with no `.nar` token and a query string (e.g. `nar/snix-castore/<blob>?narsize=7415800`), `Compression: none`, and a valid `NarHash`
- **THEN** the request SHALL succeed rather than returning an `invalid nar URL` error
- **AND** the upstream GET SHALL target the original path with its query string preserved verbatim (e.g. `?narsize=7415800`)
- **AND** the parsed compression SHALL be `none`
- **AND** the served narinfo `URL:` SHALL be ncps's own hash-named `nar/<narhash>.nar` with `Compression: none`

#### Scenario: Attic bare-.nar opaque URL keeps its declared compression

- **WHEN** an upstream narinfo has an opaque `URL:` ending in a bare `.nar` (e.g. `nar/<storePathHash>.nar`), `Compression: zstd`, and a valid `NarHash`
- **THEN** the parsed compression SHALL be `zstd`, taken from the `Compression:` header rather than from the absent URL extension
- **AND** the served narinfo `URL:` SHALL be ncps's own hash-named `nar/<narhash>.nar.zst` with `Compression: zstd`
- **AND** the NAR bytes SHALL be fetched from the original opaque upstream path

#### Scenario: Conventional hash-named upstream URL is unaffected

- **WHEN** an upstream narinfo has a conventional hash-named `URL:`
- **THEN** it SHALL be parsed and served exactly as before
- **AND** no opaque upstream path SHALL be recorded

#### Scenario: Opaque URL without a usable NarHash is rejected

- **WHEN** an upstream narinfo has an opaque `URL:` (with or without a `.nar` token) but no valid fallback `NarHash`
- **THEN** the system SHALL return a parse error
- **AND** SHALL NOT fabricate a storage key
