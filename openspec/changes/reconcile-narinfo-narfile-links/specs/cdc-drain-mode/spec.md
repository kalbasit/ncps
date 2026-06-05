## ADDED Requirements

### Requirement: Drain completion MUST be reachable for chunked NARs whose join link was never created

Drain mode is exited once no chunked `nar_file` rows remain (every NAR has been migrated to whole-file storage). A chunked NAR whose `narinfo_nar_files` link was never created MUST NOT be able to permanently block drain completion: the de-chunk migration SHALL be able to drain it (resolving its verification NarHash via the narinfo URL), and fsck SHALL repair its missing link rather than deleting the narinfo. The system SHALL NOT silently leave such NARs chunked forever.

#### Scenario: An unlinked chunked NAR does not permanently block drain

- **GIVEN** the cache is in drain mode (`cdcEnabled=false`, chunk store present)
- **AND** a chunked `nar_file` for hash `H` with intact chunks but no `narinfo_nar_files` link
- **AND** a narinfo whose URL is `nar/<H>.nar`
- **WHEN** the `migrate-chunks-to-nar` pass runs
- **THEN** `H` SHALL be de-chunked to whole-file storage
- **AND** SHALL count toward drain completion (the remaining-chunked count decreases)
