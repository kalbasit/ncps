## ADDED Requirements

### Requirement: Drain completion MUST be reachable for chunked NARs whose join link was never created

Drain mode is exited automatically: `initCDCDrainMode` runs at startup, counts chunked `nar_file` rows, and when the count is zero clears the stored CDC config and does not initialize the chunk store (the drain is complete). A chunked NAR whose `narinfo_nar_files` link was never created MUST NOT be able to permanently block this completion: the de-chunk migration SHALL be able to drain it (resolving its verification NarHash via the narinfo URL), and fsck SHALL repair its missing link rather than deleting the narinfo. The system SHALL NOT silently leave such NARs chunked forever.

#### Scenario: An unlinked chunked NAR does not permanently block drain

- **GIVEN** the cache is in drain mode (`cdcEnabled=false`, chunk store present)
- **AND** a chunked `nar_file` for hash `H` with intact chunks but no `narinfo_nar_files` link
- **AND** a narinfo whose URL is `nar/<H>.nar`
- **WHEN** the `migrate-chunks-to-nar` pass runs
- **THEN** `H` SHALL be de-chunked to whole-file storage
- **AND** SHALL count toward drain completion (the remaining-chunked count decreases)

#### Scenario: Drain auto-completes on the next boot once no chunked NARs remain

- **GIVEN** every chunked NAR has been migrated to whole-file storage (chunked count is zero)
- **WHEN** an ncps instance starts
- **THEN** `initCDCDrainMode` SHALL clear the stored CDC config
- **AND** SHALL NOT initialize the chunk store (drain mode is not entered)
- **AND** no chart/config edit SHALL be required to exit drain mode
