## MODIFIED Requirements

### Requirement: fsck MUST repair an unlinked narinfo with a backing nar_file instead of deleting it

When `fsck --repair` finds a narinfo with no `narinfo_nar_files` link, it previously deleted the narinfo as an orphan. Because a known race leaves valid, reachable narinfos unlinked from an *existing* nar_file, that deletion destroys live metadata and orphans the NAR. The system SHALL, before deleting such a narinfo, attempt to recreate the missing link: it SHALL parse the narinfo's URL, look up a `nar_file` matching that URL's hash and query (compression-agnostic), and — when one exists — create the `narinfo_nar_files` link and preserve the narinfo. The system SHALL delete the narinfo only when no backing `nar_file` exists for its URL anywhere in the database.

#### Scenario: Unlinked narinfo with a present backing nar_file is repaired

- **GIVEN** a narinfo with URL `nar/<H>.nar` and no `narinfo_nar_files` link
- **AND** a `nar_file` with hash `<H>` present in the database
- **WHEN** `fsck` runs the repair phase (not `--dry-run`)
- **THEN** the narinfo SHALL still exist after the run
- **AND** a `narinfo_nar_files` link SHALL now connect that narinfo and that `nar_file`

#### Scenario: Unlinked narinfo with no backing nar_file is still deleted

- **GIVEN** a narinfo with URL `nar/<H>.nar` and no `narinfo_nar_files` link
- **AND** NO `nar_file` matching `<H>` anywhere in the database
- **WHEN** `fsck` runs the repair phase (not `--dry-run`)
- **THEN** the narinfo SHALL be deleted (genuine orphans are reclaimed)
