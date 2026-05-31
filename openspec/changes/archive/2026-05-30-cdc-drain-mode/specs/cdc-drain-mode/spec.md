## ADDED Requirements

### Requirement: Chunk reads MUST be gated on chunk-store availability, not CDC write-enabled state

The system SHALL gate chunk-read operations (servability check, HasNarInChunks, GetNarInfo
normalization) solely on whether a chunk store is initialized (`chunkStore != nil`),
independently of the CDC write-enabled flag (`cdcEnabled`). The write gate
(`cdcEnabled && chunkStore != nil`) remains unchanged and controls only whether new
NARs are stored as chunks.

#### Scenario: Chunked NAR is served when CDC writes are disabled but chunk store is initialized

- **GIVEN** the cache is configured with `cdcEnabled=false` and a non-nil chunk store
- **AND** a `nar_file` record for hash `H` has `total_chunks > 0`
- **WHEN** `GetNar` is called for `H`
- **THEN** the system SHALL treat `H` as servable from the chunk store
- **AND** SHALL stream `H` from its chunks
- **AND** SHALL NOT attempt to re-fetch `H` from upstream

#### Scenario: New NAR is stored as whole file when CDC writes are disabled

- **GIVEN** the cache is configured with `cdcEnabled=false` and a non-nil chunk store
- **WHEN** a new NAR `H` is fetched from upstream
- **THEN** the system SHALL store `H` as a whole file in the NAR store
- **AND** SHALL NOT create any chunk records for `H`

### Requirement: The server MUST enter drain mode when `cdc.enabled: false` but chunked NARs may exist

When the server starts with `cdc.enabled: false` and the stored database configuration
has `cdc_enabled=true` (indicating CDC was previously active and chunked NARs may
remain), the server SHALL enter drain mode by initializing the chunk store for reads
without enabling chunk writes. Drain mode SHALL:

- Initialize the chunk store backend (local or S3) using the same storage flags as
  a CDC-enabled deployment.
- Leave `cdcEnabled=false` so no new NAR is stored as chunks.
- Leave the four stored CDC config keys (`cdc_enabled`, `cdc_min`, `cdc_avg`,
  `cdc_max`) intact in the database so `migrate-chunks-to-nar` can run concurrently.
- Log a warning with the count of remaining chunked NARs.

#### Scenario: Server enters drain mode on startup

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **AND** the server starts with `cdc.enabled: false`
- **THEN** the server SHALL initialize the chunk store
- **AND** SHALL set `cdcEnabled=false` (write gate off)
- **AND** SHALL NOT clear `cdc_enabled` or any other CDC config key from the database
- **AND** SHALL log a warning with the count of `nar_file` rows where `total_chunks > 0`
- **AND** SHALL start successfully

#### Scenario: `migrate-chunks-to-nar` runs concurrently with a drain-mode server

- **GIVEN** the server is running in drain mode (chunk store initialized, writes disabled)
- **WHEN** `migrate-chunks-to-nar` is executed against the same database
- **THEN** it SHALL find `cdc_enabled=true` in the database and proceed
- **AND** SHALL reconstruct and verify whole NARs from chunks
- **AND** SHALL NOT conflict with in-flight chunk-serve requests from the running server

#### Scenario: Drain mode auto-completes when no chunked NARs remain

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **AND** the server starts with `cdc.enabled: false`
- **AND** no `nar_file` row has `total_chunks > 0` (drain is complete)
- **THEN** the server SHALL clear all four stored CDC config keys from the database
- **AND** SHALL NOT initialize the chunk store
- **AND** SHALL log that the CDC drain is complete and the stored config has been cleared
- **AND** SHALL start in fully-disabled mode (no chunk reads, no chunk writes)

#### Scenario: Server with no prior CDC history starts with `cdc.enabled: false`

- **GIVEN** `cdc_enabled` is absent from the configuration database (CDC was never used)
- **AND** the server starts with `cdc.enabled: false`
- **THEN** the server SHALL NOT initialize the chunk store
- **AND** SHALL start normally with no CDC-related warnings

### Requirement: `migrate-chunks-to-nar` MUST use chunk count as the migration guard, not the DB enabled flag

The `migrate-chunks-to-nar` command SHALL determine whether migration is needed by
querying the count of `nar_file` rows with `total_chunks > 0`, not by checking
`cdc_enabled` in the configuration database. If the count is zero, the command SHALL
report that there is nothing to migrate and exit with status 0. If the count is
positive, the command SHALL proceed with migration regardless of the value of
`cdc_enabled` in the database.

#### Scenario: Migration proceeds when drain mode server cleared the DB write flag

- **GIVEN** the server was restarted with `cdc.enabled: false`
- **AND** chunked `nar_file` rows still exist (`total_chunks > 0`)
- **WHEN** `migrate-chunks-to-nar` is executed
- **THEN** it SHALL proceed with migration
- **AND** SHALL NOT fail with "CDC was never enabled in the database"

#### Scenario: Migration exits cleanly when no chunked NARs exist

- **GIVEN** `cdc_enabled` is absent from the database OR set to any value
- **AND** no `nar_file` row has `total_chunks > 0`
- **WHEN** `migrate-chunks-to-nar` is executed
- **THEN** it SHALL report that there is nothing to migrate
- **AND** SHALL exit with status 0
