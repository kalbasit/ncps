## ADDED Requirements

### Requirement: nar_file MUST carry a residue-flag timestamp for deferred reclamation

The `nar_file` table SHALL have a nullable `dechunk_residue_flagged_at` timestamp column. It records when fsck first observed the row to be an un-de-chunkable chunked NAR (CDC residue). A null value means the row is not currently flagged. fsck sets it on first detection, clears it when the row becomes recoverable or is de-chunked, and uses its age (against a grace window) to decide deferred purge. The column is added by a forward-only additive migration and is independent of `verified_at`, `bytes_stored_at`, and `chunking_started_at`.

#### Scenario: The column is nullable and defaults to unset

- **GIVEN** a freshly created `nar_file` row
- **THEN** its `dechunk_residue_flagged_at` SHALL be null (not flagged)
