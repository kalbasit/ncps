## MODIFIED Requirements

### Requirement: Opaque (non hash-named) upstream NAR URLs MUST be tolerated

The system MUST proxy an upstream narinfo whose `URL:` field is not a conventional
hash-named `nar/<hash>.nar[.<compression>]` path rather than rejecting it. This
covers two opaque shapes:

1. **Non-hash filename that still ends in `.nar[.<compression>]`** — e.g. cachix's
   `nar/<uuidv4>.nar.zst` (the stem before `.nar` is not a valid Nix hash).
2. **No `.nar` token at all** — e.g. snix-castore's
   `nar/snix-castore/<base64-blob>?narsize=N`, served with `Compression: none`.

For any opaque URL the system SHALL derive its local storage key from the narinfo
`NarHash` (re-encoded as a bare nix32 digest), SHALL preserve the original opaque
upstream path **including its query string** verbatim for the upstream GET, and
SHALL re-serve the NAR to downstream clients under its own hash-named URL keyed off
the `NarHash`. For a `.nar`-less opaque URL the compression SHALL be taken as `none`
(the URL carries no compression extension and such upstreams advertise
`Compression: none`). Conventional hash-named upstream URLs SHALL continue to be
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

#### Scenario: Conventional hash-named upstream URL is unaffected

- **WHEN** an upstream narinfo has a conventional hash-named `URL:`
- **THEN** it SHALL be parsed and served exactly as before
- **AND** no opaque upstream path SHALL be recorded

#### Scenario: Opaque URL without a usable NarHash is rejected

- **WHEN** an upstream narinfo has an opaque `URL:` (with or without a `.nar` token) but no valid fallback `NarHash`
- **THEN** the system SHALL return a parse error
- **AND** SHALL NOT fabricate a storage key
