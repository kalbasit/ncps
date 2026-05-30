## ADDED Requirements

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
