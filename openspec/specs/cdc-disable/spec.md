# Capability Spec: CDC Disable

## Purpose

Defines requirements for disabling Content-Defined Chunking (CDC) after it has been
previously enabled, including the startup transition behavior, stored config cleanup,
and how un-migrated chunked NARs are handled during subsequent read requests.

## Requirements

### Requirement: CDC MAY be disabled after being enabled when migrate-chunks-to-nar has been run

The system SHALL allow transitioning from `cdc_enabled=true` (stored in DB) to `cdc.enabled: false` (current config) without returning a startup error. When this transition is detected, the system SHALL:
- Clear the four stored CDC config keys (`cdc_enabled`, `cdc_min`, `cdc_avg`, `cdc_max`) from the configuration database.
- Log a structured warning if any `nar_file` rows with `total_chunks > 0` remain, including the count of un-migrated NARs.
- Proceed with startup normally regardless of whether un-migrated chunks remain.

#### Scenario: Clean disable — all NARs migrated

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **AND** all `nar_file` rows have `total_chunks = 0` (migration complete)
- **WHEN** the server starts with `cdc.enabled: false`
- **THEN** the server SHALL start successfully
- **AND** the stored CDC config keys SHALL be cleared from the database
- **AND** no warning about un-migrated chunks SHALL be logged

#### Scenario: Partial disable — un-migrated chunks remain

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **AND** at least one `nar_file` row still has `total_chunks > 0`
- **WHEN** the server starts with `cdc.enabled: false`
- **THEN** the server SHALL start successfully
- **AND** the stored CDC config keys SHALL be cleared from the database
- **AND** a warning log entry SHALL be emitted that includes the count of remaining chunked NARs
- **AND** the warning SHALL reference `migrate-chunks-to-nar` as the remediation command

#### Scenario: Re-enable after disable is treated as a fresh first boot

- **GIVEN** CDC was previously disabled (stored config keys cleared)
- **WHEN** the server starts with `cdc.enabled: true` and new chunk sizes
- **THEN** the server SHALL store the new CDC configuration (enabled=true, min, avg, max)
- **AND** no mismatch error SHALL be returned for the new chunk sizes

### Requirement: Chunked NARs become cache misses when CDC is disabled

After CDC is disabled, any `nar_file` rows with `total_chunks > 0` that were not migrated SHALL NOT be served from the chunk store (CDC is off). A client request for such a NAR SHALL trigger the normal cache-miss-recovery path (upstream re-fetch), treating the NAR as absent from local storage.

#### Scenario: Un-migrated chunked NAR triggers upstream re-fetch

- **GIVEN** CDC is disabled
- **AND** a `nar_file` row for hash `H` has `total_chunks > 0`
- **AND** no whole-file for `H` exists in the NAR store
- **WHEN** a client requests `GET /nar/H...`
- **THEN** the system SHALL treat `H` as a cache miss
- **AND** SHALL attempt to re-fetch `H` from upstream caches
- **AND** SHALL NOT attempt to reconstruct `H` from chunks
