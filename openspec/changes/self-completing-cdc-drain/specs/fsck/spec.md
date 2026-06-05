## ADDED Requirements

### Requirement: fsck MUST heal inconsistent or un-de-chunkable chunked NARs

`fsck --repair` SHALL detect chunked `nar_file` rows that are inconsistent (their narinfo advertises a compression that does not match the chunked-none representation) or un-de-chunkable (no narinfo carries a resolvable NarHash, or the chunks are missing/corrupt) and heal them: relink and normalize the narinfo URL where a valid NarHash exists, or purge the chunked `nar_file` (so a later request re-pulls) where it does not. fsck SHALL serve as the safety net for any residue the de-chunk migration's purge-on-unverifiable did not reach.

#### Scenario: fsck normalizes a recoverable inconsistent chunked NAR

- **GIVEN** a chunked `nar_file` whose narinfo advertises a different-compression URL but carries a valid NarHash
- **WHEN** `fsck --repair` runs
- **THEN** it SHALL relink the narinfo and normalize its URL to the Compression:none form

#### Scenario: fsck purges an un-de-chunkable chunked NAR

- **GIVEN** a chunked `nar_file` with no narinfo carrying a resolvable NarHash
- **WHEN** `fsck --repair` runs
- **THEN** it SHALL purge the chunked `nar_file`
- **AND** SHALL NOT leave it chunked
