# NAR Cache Miss Recovery

## Purpose

Defines requirements for treating backing-less and stuck `nar_file` records as
recoverable cache misses rather than terminal 404s, so that transient upstream
failures and incomplete chunking do not permanently poison a NAR.
## Requirements
### Requirement: A backing-less nar_file record MUST be treated as a cache miss, not a 404

The system SHALL treat a `GetNar` request for a NAR whose `nar_file` row exists but has **no
backing data** — no whole-file in the store, `total_chunks = 0`, and chunking is not actively in
progress (`chunking_started_at` is NULL or older than the chunking lock TTL) — as a cache **miss**
and attempt a synchronous upstream re-download. It SHALL NOT return `storage.ErrNotFound`
(HTTP 404) on the basis that the row exists.

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

The system SHALL NOT leave behind a `nar_file` record that causes future `GetNar` calls to return
a terminal 404 when a background or synchronous NAR download fails for a transient reason
(connection reset, HTTP/2 `GOAWAY`, `http2: timeout awaiting response headers`, broken pipe, or any
non-404 error). A later request for the same hash SHALL be able to re-attempt the upstream download.

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

### Requirement: A narinfo whose NAR is missing from storage MUST trigger recovery, not a destructive 404

The system SHALL treat a narinfo whose backing NAR is absent from storage as a cache miss and recover the NAR from upstream on demand, rather than returning HTTP 404 or destructively purging the narinfo. It SHALL NOT respond to the in-flight or any subsequent `GET /nar/<hash>...` request with `storage.ErrNotFound` (HTTP 404) solely because the bytes are currently absent, and it SHALL NOT take a destructive action (purging the narinfo) that leaves the system unable to re-download the NAR.

This requirement covers the storage-absence case specifically — a narinfo that
is present and otherwise valid, whose whole-file and chunks are both missing
from the store (e.g. removed by GC, lost by the backend, or never durably
written) — as distinct from a backing-less `nar_file` row with `total_chunks=0`.

#### Scenario: Missing-NAR narinfo request recovers from upstream instead of 404

- **GIVEN** a narinfo for hash `H` exists in the database
- **AND** neither a whole-file nor any chunks for `H` exist in storage
- **AND** the upstream has the NAR for `H`
- **WHEN** a client requests `GET /nar/<H>.nar.xz`
- **THEN** the system SHALL initiate an upstream download for `H`
- **AND** SHALL stream the recovered NAR bytes to the client
- **AND** SHALL NOT return HTTP 404

#### Scenario: Detecting a missing NAR does not poison future requests

- **GIVEN** a narinfo for hash `H` whose NAR is missing from storage
- **WHEN** the system detects the missing NAR (the condition that previously
  logged "narinfo was found in the database but no nar was found in storage,
  requesting a purge")
- **THEN** any record mutation it performs SHALL leave `H` in a state from which
  a later `GET /nar/<H>...` re-downloads from upstream
- **AND** SHALL NOT negative-cache `H` such that a subsequent request
  short-circuits to HTTP 404

#### Scenario: NAR genuinely absent upstream still returns 404

- **GIVEN** a narinfo for hash `H` whose NAR is missing from storage
- **AND** every configured upstream returns not-found for `H`
- **WHEN** the system attempts recovery for `H`
- **THEN** it SHALL return `storage.ErrNotFound` (HTTP 404)
- **AND** SHALL NOT persist a record that would prevent a future successful
  download of `H` once it reappears upstream

#### Scenario: Transient storage read error is not mistaken for a missing NAR

- **GIVEN** a narinfo for hash `H` whose NAR is present in storage
- **AND** a storage stat/read for `H` fails transiently (e.g. a timeout or a
  stale-metadata read on a network filesystem) rather than returning a definite
  not-found
- **WHEN** the system evaluates whether the NAR is missing
- **THEN** it SHALL NOT treat an ambiguous/transient storage error as a
  confirmed absence
- **AND** SHALL NOT purge the narinfo on the basis of that ambiguous result

