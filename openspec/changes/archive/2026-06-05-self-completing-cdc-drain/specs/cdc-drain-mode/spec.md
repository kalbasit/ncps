## ADDED Requirements

### Requirement: A full de-chunk pass MUST leave drain able to auto-complete

After a single `migrate-chunks-to-nar` pass completes over all chunked NARs, no chunked `nar_file` row SHALL remain. Consequently the next ncps startup's `initCDCDrainMode` SHALL observe a zero chunked count and auto-complete the drain (clear the stored CDC config, skip chunk-store init) without any manual data cleanup. CDC residue (mismatched narinfo URLs, missing join links, un-verifiable or unreconstructable chunked NARs) SHALL NOT be able to permanently strand drain mode.

#### Scenario: Drain auto-completes after one de-chunk pass over residue

- **GIVEN** a cache in drain mode whose chunked set includes residue NARs (different-compression narinfo URLs, missing links, corrupt chunks)
- **WHEN** one `migrate-chunks-to-nar` pass runs to completion
- **THEN** the chunked `nar_file` count SHALL be zero
- **AND** the next ncps startup SHALL auto-complete the drain with no manual SQL
