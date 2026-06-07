# Capability Spec: CDC Disable

## Purpose

Defines requirements for disabling Content-Defined Chunking (CDC) after it has been
previously enabled, including the startup drain-mode transition behavior, stored config
preservation, and how un-migrated chunked NARs are served during the drain period.

## Requirements

### Requirement: CDC MAY be disabled after being enabled — chunked NARs continue to serve during drain

The system SHALL allow transitioning from `cdc_enabled=true` (stored in the database)
to `cdc.enabled: false` (current configuration) without a startup error, and SHALL
continue serving existing chunked NARs from the chunk store during the drain period.
This replaces the previous hard-cutover semantics where chunked NARs became cache misses.

When this transition is detected at startup, the system SHALL:
- Initialize the chunk store backend for read access.
- Disable chunk writes (`cdcEnabled=false`), so new NARs are stored as whole files.
- Preserve the four stored CDC config keys in the database.
- Log a structured warning if any `nar_file` rows with `total_chunks > 0` remain.
- Start successfully regardless of whether un-migrated chunks remain.

#### Scenario: Clean disable — all NARs migrated

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **AND** all `nar_file` rows have `total_chunks = 0` (migration complete)
- **WHEN** the server starts with `cdc.enabled: false`
- **THEN** the server SHALL start successfully
- **AND** the stored CDC config keys SHALL be auto-cleared from the database
- **AND** no warning about un-migrated chunks SHALL be logged

#### Scenario: Drain mode — un-migrated chunks remain, still served

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **AND** at least one `nar_file` row still has `total_chunks > 0`
- **WHEN** the server starts with `cdc.enabled: false`
- **THEN** the server SHALL start successfully in drain mode
- **AND** the stored CDC config keys SHALL remain intact
- **AND** a warning log entry SHALL be emitted with the count of remaining chunked NARs
- **AND** a subsequent `GetNar` for a still-chunked hash SHALL serve from the chunk store
- **AND** a subsequent `GetNar` for a hash NOT in chunks SHALL follow the normal whole-file or upstream path

### Requirement: Chunked NARs are served from the chunk store during drain, not treated as cache misses

This requirement SHALL replace "Chunked NARs become cache misses when CDC is disabled" (removed).

After CDC is disabled with chunked NARs remaining, those NARs SHALL be served from the
chunk store until they are migrated to whole files by `migrate-chunks-to-nar`. The chunk
store SHALL remain initialized and readable throughout the drain period.

#### Scenario: Chunked NAR is served during drain without upstream re-fetch

- **GIVEN** the server is in drain mode (CDC writes disabled, chunk store initialized)
- **AND** a `nar_file` record for hash `H` has `total_chunks > 0`
- **AND** no whole-file for `H` exists in the NAR store
- **WHEN** a client requests `GET /nar/H...`
- **THEN** the system SHALL serve `H` from its chunks
- **AND** SHALL NOT attempt to re-fetch `H` from upstream caches

#### Scenario: After migration, the same NAR is served as a whole file

- **GIVEN** the server is in drain mode
- **AND** `migrate-chunks-to-nar` has flipped hash `H` to `total_chunks = 0`
- **AND** a whole file for `H` now exists in the NAR store
- **WHEN** a client requests `GET /nar/H...`
- **THEN** the system SHALL serve `H` from the whole file in the NAR store
