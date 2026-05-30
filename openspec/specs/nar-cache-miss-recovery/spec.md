# NAR Cache Miss Recovery

## Purpose

Defines requirements for treating backing-less and stuck `nar_file` records as
recoverable cache misses rather than terminal 404s, so that transient upstream
failures and incomplete chunking do not permanently poison a NAR.

## Requirements

### Requirement: A backing-less nar_file record MUST be treated as a cache miss, not a 404

When `GetNar` is called for a NAR whose `nar_file` row exists but has **no backing data** —
no whole-file in the store, `total_chunks = 0`, and chunking is not actively in progress
(`chunking_started_at` is NULL or older than the chunking lock TTL) — the system SHALL treat
the request as a cache **miss** and attempt a synchronous upstream re-download. It SHALL NOT
return `storage.ErrNotFound` (HTTP 404) on the basis that the row exists.

A NAR is "servable" only when at least one of these holds: a whole-file exists in the store,
`total_chunks > 0`, or chunking is actively in progress within the lock TTL. The mere
existence of a `nar_file` row (e.g. via a `HasNarFileRecord`-style check) SHALL NOT by itself
make a NAR servable.

#### Scenario: Backing-less record triggers re-download instead of 404

- **GIVEN** CDC is enabled
- **AND** a `nar_file` row exists for hash `H` with `total_chunks = 0` and `chunking_started_at` NULL
- **AND** no whole-file `.nar`/`.nar.xz` for `H` exists in the store
- **AND** no chunks exist for `H`
- **WHEN** `GetNar` is called for `H`
- **THEN** the system SHALL initiate a synchronous upstream download for `H`
- **AND** SHALL NOT return `storage.ErrNotFound` solely because the row exists

#### Scenario: Successful re-download serves the NAR and heals the record

- **GIVEN** the conditions of the previous scenario
- **AND** the upstream has the NAR for `H`
- **WHEN** `GetNar` re-downloads `H`
- **THEN** the NAR bytes SHALL be streamed to the client
- **AND** the `nar_file` record SHALL end in a servable state (whole-file stored and/or `total_chunks > 0`)
- **AND** subsequent `GetNar` calls for `H` SHALL be served from cache without a 404

#### Scenario: NAR genuinely absent upstream returns 404

- **GIVEN** a backing-less `nar_file` row for hash `H`
- **AND** the upstream returns 404 / not-found for `H`
- **WHEN** `GetNar` is called for `H`
- **THEN** the system SHALL return `storage.ErrNotFound` (HTTP 404)
- **AND** SHALL NOT persist a record that would prevent a future successful download of `H`

### Requirement: A transient upstream failure MUST NOT permanently poison a NAR

When a background or synchronous NAR download fails for a transient reason (connection reset,
HTTP/2 `GOAWAY`, `http2: timeout awaiting response headers`, broken pipe, or any non-404 error),
the system SHALL NOT leave behind a `nar_file` record that causes future `GetNar` calls to return
a terminal 404. A later request for the same hash SHALL be able to re-attempt the upstream download.

#### Scenario: Transient download failure is retryable on next request

- **GIVEN** a NAR download for hash `H` fails with a transient upstream error
- **WHEN** a client later requests `GET /nar/H...`
- **THEN** `GetNar` SHALL re-attempt the upstream download for `H`
- **AND** SHALL NOT short-circuit to `storage.ErrNotFound` because of the prior failure's leftover record

#### Scenario: Recovery sweep re-drives stuck records

- **GIVEN** one or more `nar_file` rows are stuck (`total_chunks=0`, chunking not progressing)
- **WHEN** the CDC lazy-recovery job runs
- **THEN** it SHALL re-drive rows that have a whole-file in the store to a servable (chunked) state
- **AND** it MAY skip backing-less rows that lack a whole-file, leaving them for on-demand recovery
  by `GetNar` (which re-downloads from upstream)
- **AND** SHALL NOT indefinitely retry a hash that upstream genuinely does not have
- **AND** skipped backing-less rows SHALL NOT block the sweep from reaching genuinely re-drivable
  rows on subsequent runs (no head-of-line starvation)
