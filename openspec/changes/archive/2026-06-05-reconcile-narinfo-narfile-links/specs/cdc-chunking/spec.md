## ADDED Requirements

### Requirement: Completing a CDC chunking operation MUST reconcile the narinfo↔nar_file link

The `narinfo_nar_files` link is created in the narinfo-write path, which can race the asynchronous chunking that finalizes the `nar_file` row and leave the chunked `nar_file` unlinked. After a chunking operation finalizes a `nar_file` (and on the post-store narinfo reconciliation it triggers), the system SHALL ensure that every narinfo whose URL references that NAR is linked to the finalized `nar_file`, creating the `narinfo_nar_files` link when missing. The reconciliation SHALL be idempotent — when the link already exists it is a no-op — so it does not alter steady-state behavior.

#### Scenario: Chunking completion creates the missing link

- **GIVEN** a finalized chunked `nar_file` for hash `H` (`total_chunks > 0`)
- **AND** a narinfo whose URL is `nar/<H>.nar`
- **AND** no `narinfo_nar_files` link between them
- **WHEN** `checkAndFixNarInfosForNar` runs for the NAR (as invoked on chunking completion)
- **THEN** a `narinfo_nar_files` link SHALL be created between that narinfo and the `nar_file`

#### Scenario: Reconciliation is a no-op when the link already exists

- **GIVEN** a `nar_file` for hash `H` already linked to its narinfo
- **WHEN** `checkAndFixNarInfosForNar` runs for the NAR
- **THEN** the existing link SHALL be preserved unchanged
- **AND** no duplicate link SHALL be created
