## ADDED Requirements

### Requirement: fsck MUST normalize a recoverable inconsistent chunked NAR immediately

When fsck `--repair` finds a chunked `nar_file` whose narinfo references it with a valid NarHash but advertises a non-Compression:none URL, it SHALL relink (if needed) and normalize that narinfo's URL to the Compression:none form. This operation touches no chunks and SHALL be performed regardless of CDC state. If the row carried a residue flag, fsck SHALL clear it.

#### Scenario: Recoverable inconsistent chunked NAR is normalized, not purged

- **GIVEN** a chunked `nar_file` for hash `H` whose narinfo has a valid NarHash but URL `nar/<H>.nar.xz`
- **WHEN** `fsck --repair` runs
- **THEN** the narinfo SHALL be normalized to URL `nar/<H>.nar` (Compression none)
- **AND** the `nar_file` SHALL remain chunked (not purged)

### Requirement: fsck MUST mark, not immediately purge, an un-de-chunkable chunked NAR

When fsck `--repair` finds a chunked `nar_file` with no narinfo carrying a resolvable NarHash (un-de-chunkable), it SHALL NOT purge it on first detection. Instead it SHALL record a persistent flag (`dechunk_residue_flagged_at`) if not already set. fsck SHALL purge the row only on a later run, once the flag has aged past a configurable grace window (default ~24h) AND the row is still un-de-chunkable at that later run. If the row becomes recoverable or de-chunked before the grace window elapses, fsck SHALL clear the flag and SHALL NOT purge it. This two-run, grace-windowed reclamation prevents purging transient states (a NAR mid-chunking, a narinfo not yet written) and protects legitimately chunked NARs.

#### Scenario: First detection flags but does not purge

- **GIVEN** a chunked `nar_file` for hash `H` with no resolvable NarHash and no residue flag
- **WHEN** `fsck --repair` runs
- **THEN** `H`'s `dechunk_residue_flagged_at` SHALL be set
- **AND** `H` SHALL NOT be purged

#### Scenario: Aged, still-un-de-chunkable row is purged on a later run

- **GIVEN** a chunked `nar_file` for hash `H` with `dechunk_residue_flagged_at` older than the grace window
- **AND** `H` is still un-de-chunkable (no resolvable NarHash)
- **WHEN** `fsck --repair` runs
- **THEN** `H` SHALL be purged

#### Scenario: A row that became recoverable is unflagged, never purged

- **GIVEN** a chunked `nar_file` for hash `H` previously flagged
- **AND** `H` now has a narinfo with a resolvable NarHash
- **WHEN** `fsck --repair` runs
- **THEN** fsck SHALL clear `H`'s residue flag
- **AND** SHALL NOT purge `H`
