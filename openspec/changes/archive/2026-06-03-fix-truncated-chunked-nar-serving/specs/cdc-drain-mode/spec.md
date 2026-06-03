## ADDED Requirements

### Requirement: Drain and migrate MUST skip and report un-reassemblable chunked NARs rather than aborting

The system SHALL skip un-reassemblable chunked NARs and continue the migration
instead of aborting the whole run on the first failure. When `migrate-chunks-to-nar`
(run standalone or concurrently with a drain-mode server) encounters a chunked NAR
that cannot be reassembled — because its `nar_file_chunks` links are incomplete
(`links < total_chunks`) or because a referenced chunk's blob is absent from the
chunk store — it MUST skip that NAR and continue migrating the remaining NARs. The
command MUST count and report the un-reassemblable NARs (by hash) at the end of the
run so the operator can act, and its exit behavior MUST distinguish "all
reassemblable NARs migrated, some skipped" from "fatal error".

#### Scenario: Migration skips an un-reassemblable NAR and continues

- **GIVEN** drain mode is active and chunked `nar_file` rows exist
- **AND** one such NAR `H` is missing one or more chunk links or chunk blobs
- **WHEN** `migrate-chunks-to-nar` is executed
- **THEN** it SHALL skip `H` without aborting the run
- **AND** it SHALL continue migrating the other reassemblable NARs to whole files
- **AND** it SHALL include `H` in a report of skipped/un-reassemblable NARs

#### Scenario: Migration reports the count of skipped NARs

- **GIVEN** `K` chunked NARs cannot be reassembled and the rest can
- **WHEN** `migrate-chunks-to-nar` completes
- **THEN** it SHALL report the number of NARs migrated and the number skipped (`K`)
- **AND** it SHALL list the skipped NAR hashes so they can be purged and refetched

### Requirement: An un-reassemblable chunked NAR MUST be purgeable to a clean cache-miss state

The system SHALL provide a path by which a permanently un-reassemblable chunked NAR
(missing links or blobs, confirmed not in progress) is removed from the cache —
deleting its `nar_file` row and chunk links while retaining the linked narinfo —
so that a subsequent `GetNar` request observes a clean cache miss (the NAR is
neither servable from chunks nor present as a whole file). The standard
cache-miss path then refetches `H` from a configured upstream, which makes the 404
produced by the serving-integrity check self-healing rather than a permanent dead
end. (The upstream-fetch-and-serve-on-miss behavior itself is the cache's general
miss path, covered by existing `GetNar` tests; this requirement governs only that
the purge leaves a clean miss.)

#### Scenario: Purging an un-reassemblable NAR leaves a clean cache miss

- **GIVEN** a completed chunked NAR `H` that cannot be reassembled
- **WHEN** `H` is purged (via the migrate/drain path or an explicit purge)
- **THEN** `H`'s `nar_file` record and chunk links SHALL be deleted
- **AND** the cache SHALL report `H` as neither servable from chunks nor present as a whole file (a cache miss)
- **AND** the linked narinfo SHALL be retained so the next request can refetch `H` from upstream
