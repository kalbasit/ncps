## ADDED Requirements

### Requirement: The backing-less GC MUST NOT leave a dangling narinfo

When the recovery sweep deletes a backing-less `nar_file` because every linked narinfo is genuinely absent from every healthy upstream, the system SHALL delete those linked narinfos in the same transaction and MUST NOT rely on the `narinfo_nar_files` cascade alone (which leaves the narinfos dangling).

The cascade on `narinfo_nar_files` removes only the link rows; the parent narinfos
survive linked to no `nar_file`. Such a narinfo is unservable locally and confirmed
absent from every healthy upstream, so it can only ever be answered with an HTTP 200
narinfo followed by an HTTP 404 NAR (which Nix clients have already cached and will
fetch directly), and therefore MUST NOT be retained.

A narinfo that is unservable locally and confirmed absent from every healthy
upstream is unrecoverable: it can only ever be answered with an HTTP 200 narinfo
followed by an HTTP 404 NAR (which Nix clients have already cached and will fetch
directly), so it MUST NOT be retained.

This requirement applies ONLY to the genuinely-absent-upstream deletion branch.
The existing safety gate is unchanged: if any linked narinfo is Present or its
upstream existence is Unknown, or there are no healthy upstreams, the sweep SHALL
delete nothing (neither `nar_file` nor narinfos) and leave the record for
on-demand recovery. The zero-linked-narinfo branch is likewise unchanged.

#### Scenario: Genuinely-absent backing-less NAR deletes its dangling narinfos

- **GIVEN** a backing-less `nar_file` for hash `H` with no whole-file in storage and `total_chunks = 0`
- **AND** one or more narinfos linked to it via `narinfo_nar_files`
- **AND** every healthy upstream reports each linked narinfo as `ExistenceAbsent`
- **WHEN** the recovery sweep runs `gcOrSkipBackingLessNarFile` for that record
- **THEN** the `nar_file` row SHALL be deleted
- **AND** every linked narinfo SHALL also be deleted in the same transaction
- **AND** no narinfo SHALL remain in the database with a URL referencing hash `H`
- **AND** no narinfo SHALL be left without a `narinfo_nar_files` link as a result of this GC

#### Scenario: A still-present upstream aborts deletion entirely

- **GIVEN** a backing-less `nar_file` for hash `H` with one or more linked narinfos
- **AND** at least one healthy upstream reports a linked narinfo as `ExistencePresent` or `ExistenceUnknown` (or there are no healthy upstreams)
- **WHEN** the recovery sweep runs `gcOrSkipBackingLessNarFile` for that record
- **THEN** neither the `nar_file` nor any linked narinfo SHALL be deleted
- **AND** the record SHALL be left for on-demand `GetNar` recovery

#### Scenario: Orphan with no linked narinfo is still GC'd cleanly

- **GIVEN** a backing-less `nar_file` for hash `H` with zero linked narinfos
- **WHEN** the recovery sweep runs `gcOrSkipBackingLessNarFile` for that record
- **THEN** the `nar_file` row SHALL be deleted
- **AND** no narinfo deletion SHALL be attempted (there are none to delete)
