## ADDED Requirements

### Requirement: Opaque (non hash-named) upstream NAR URLs MUST be tolerated

An upstream narinfo whose `URL:` field is not hash-named (the filename before `.nar` is not a valid Nix hash — e.g. cachix's `nar/<uuidv4>.nar.zst`) SHALL be proxied successfully rather than rejected. The system SHALL derive its local storage key from the narinfo `NarHash` (re-encoded as a bare nix32 digest), SHALL preserve the original opaque upstream path verbatim for the upstream GET, and SHALL re-serve the NAR to downstream clients under its own hash-named URL keyed off the `NarHash`. Conventional hash-named upstream URLs SHALL continue to be handled exactly as before.

When an opaque upstream URL is encountered but the narinfo carries no usable `NarHash`, the system SHALL surface a parse error rather than fabricate a storage key.

#### Scenario: Opaque upstream URL is proxied successfully

- **WHEN** an upstream narinfo has an opaque `URL:` (e.g. `nar/<uuid>.nar.zst`) and a valid `NarHash`
- **THEN** the request SHALL succeed rather than failing with an invalid-nar-hash error
- **AND** the served narinfo `URL:` SHALL be ncps's own hash-named URL derived from the `NarHash`
- **AND** the NAR bytes SHALL be fetched from the original opaque upstream path

#### Scenario: Conventional hash-named upstream URL is unaffected

- **WHEN** an upstream narinfo has a conventional hash-named `URL:`
- **THEN** it SHALL be parsed and served exactly as before
- **AND** no opaque upstream path SHALL be recorded

#### Scenario: Opaque URL without a usable NarHash is rejected

- **WHEN** an upstream narinfo has an opaque `URL:` but no valid fallback `NarHash`
- **THEN** the system SHALL return a parse error
- **AND** SHALL NOT fabricate a storage key

### Requirement: The opaque upstream NAR path MUST survive local eviction

When a NAR was fetched via an opaque upstream URL, the system SHALL persist the opaque upstream path so the NAR can be re-fetched from upstream after the local copy is evicted. On a cache-miss for such a NAR, the system SHALL restore the persisted opaque path so the upstream GET targets the original upstream location rather than ncps's own hash-named URL (which exists only locally). Persisting the opaque path is best-effort: a failure to record it SHALL be logged and SHALL NOT fail the in-flight request.

#### Scenario: Evicted opaque NAR is re-fetched via the persisted path

- **GIVEN** a NAR previously fetched via an opaque upstream URL whose local bytes have been evicted
- **WHEN** the NAR is requested again
- **THEN** the system SHALL re-fetch it from upstream using the persisted opaque path
- **AND** SHALL serve the identical NAR bytes

#### Scenario: Failure to persist the opaque path does not fail the request

- **WHEN** recording the opaque upstream path fails during the pull
- **THEN** the in-flight request SHALL still succeed
- **AND** the failure SHALL be logged
