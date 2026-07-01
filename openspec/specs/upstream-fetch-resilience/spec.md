# Capability Spec: Upstream Fetch Resilience

## Purpose

Defines requirements for resilient fetching from upstream binary caches, including
retry and backoff behavior for transient transport failures.

## Requirements

### Requirement: Transient upstream transport failures MUST be retried with bounded backoff

An idempotent (GET/HEAD) upstream request that fails transiently SHALL be retried with
bounded, capped backoff. A transient transport error means HTTP/2 `GOAWAY`, `http2:
timeout awaiting response headers`, connection reset, or broken pipe. The retry count
SHALL be bounded, the per-attempt **backoff delay capped**, and the wait SHALL respect
context cancellation. A genuine not-found (HTTP 404) response is not a transport error and
SHALL NOT be retried.

#### Scenario: Transient error is retried after a delay then succeeds

- **GIVEN** an upstream GET that fails once with a transient transport error then succeeds
- **WHEN** the request is performed
- **THEN** it SHALL be retried after a backoff delay and ultimately succeed

#### Scenario: Retries are bounded and backoff is capped

- **GIVEN** an upstream GET that fails repeatedly with a transient transport error
- **WHEN** the request is performed
- **THEN** the number of retries SHALL be bounded
- **AND** the per-attempt backoff SHALL not exceed a fixed cap
- **AND** the total added latency SHALL stay within a bounded budget

#### Scenario: Context cancellation aborts the backoff wait

- **GIVEN** a transient failure has triggered a backoff wait
- **WHEN** the request context is cancelled during the wait
- **THEN** the request SHALL return promptly with the context error rather than completing the delay

#### Scenario: Genuine 404 is not retried

- **GIVEN** an upstream request whose response is HTTP 404
- **WHEN** the request is performed
- **THEN** it SHALL NOT be retried
- **AND** the not-found result SHALL be surfaced to the caller

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

### Requirement: Compressed upstream narinfos missing FileSize/FileHash MUST be tolerated and self-completed

The system SHALL NOT reject an upstream narinfo that declares a non-`none` `Compression`
(e.g. `zstd`, `xz`) but omits the optional `FileSize` and/or `FileHash` fields; it SHALL
accept it, fetch its NAR, and serve it, since those fields are optional in the narinfo
format. For a compressed NAR served under its original compression (i.e. not normalized to
`Compression: none` and not stored as CDC chunks), the system SHALL ensure the narinfo it
serves downstream carries a correct `FileSize` and `FileHash`: when upstream supplies a
`FileHash` the system SHALL preserve it unchanged, and when upstream omits either field the
system SHALL compute the missing value(s) itself from the compressed NAR bytes once the NAR
is stored — `FileSize` as the byte length of the stored compressed NAR and `FileHash` as its
SHA-256 digest (formatted as a nix `sha256:<nixbase32>` hash) — and SHALL backfill the
computed values into the persisted narinfo. The computation SHALL stream the stored
compressed NAR through a hasher (constant memory, no full-file buffering) and SHALL NOT
alter the NAR bytes, the `NarHash`, the `NarSize`, or the `Compression` advertised to
downstream clients.

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

#### Scenario: Upstream-provided FileHash is preserved, not recomputed

- **GIVEN** an upstream narinfo with `Compression: zstd` that already provides a `FileHash`
- **WHEN** the narinfo is fetched and the NAR is served
- **THEN** the served narinfo SHALL carry the upstream `FileHash` verbatim
- **AND** ncps SHALL NOT recompute it
- **AND** the `FileSize` SHALL be preserved when it matches the stored compressed bytes (an inconsistent `FileSize` is reconciled to the stored size)

#### Scenario: Uncompressed narinfos are unaffected

- **GIVEN** an upstream narinfo with `Compression: none` (or empty) and no `FileSize`/`FileHash`
- **WHEN** the narinfo is fetched
- **THEN** existing `Compression: none` handling SHALL apply unchanged
- **AND** no compressed-file hash SHALL be computed
