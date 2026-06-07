## ADDED Requirements

### Requirement: NAR deletion MUST remove the stored compression variant before clearing the bytes-stored marker

When the system deletes (evicts) a NAR, it SHALL remove the NAR's physical object under the variant it is actually stored as — in particular, for a `Compression:none` NAR it SHALL remove the `.nar.zst` variant under which `none` NARs are physically stored (mirroring how presence is determined) — **before** clearing the durable `bytes_stored_at` marker on the matching `nar_file` row. The deletion SHALL return a not-found error only when **no** variant of the NAR was present, preserving the established "deleting an absent NAR errors" contract; in that case the marker SHALL NOT be cleared. A non-not-found failure SHALL be returned and SHALL leave the marker intact (the NAR stays reported present). The marker SHALL be cleared only after at least one physical variant was successfully removed.

#### Scenario: Deleting a Compression:none NAR removes the underlying .nar.zst object

- **GIVEN** a `Compression:none` NAR whose physical object is stored under the `.nar.zst` variant with `bytes_stored_at` set
- **WHEN** the NAR is deleted via `nar/<H>.nar`
- **THEN** the `.nar.zst` object SHALL be removed from the store
- **AND** the `bytes_stored_at` marker SHALL then be cleared
- **AND** a subsequent `/upload` presence check SHALL report the NAR absent and find no orphaned object on disk

#### Scenario: Deleting a NAR absent from the store returns not-found and keeps the marker

- **GIVEN** a `Compression:none` `nar_file` row with `bytes_stored_at` set but no physical bytes (neither the `.nar.zst` variant nor the bare object) in the store
- **WHEN** the NAR is deleted
- **THEN** the deletion SHALL return a not-found error
- **AND** the `bytes_stored_at` marker SHALL remain set

#### Scenario: A real byte-deletion failure does not clear the marker

- **WHEN** removing the NAR's physical bytes fails with a non-not-found error
- **THEN** the deletion SHALL return that error
- **AND** the `bytes_stored_at` marker SHALL remain set (the NAR is still reported present)
