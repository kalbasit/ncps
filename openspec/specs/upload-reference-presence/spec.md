# upload-reference-presence Specification

## Purpose

Defines how the upload-only (`/upload`) path decides a narinfo's backing NAR is present, so a `nix copy --to .../upload` reference check does not 404 a NAR that is durably stored but not yet locally visible. The rules trust a shared-database `bytes_stored_at` marker (set by `PutNar`) over a single replica's local filesystem `stat`, match the NAR compression-agnostically, and exclude byte-less placeholders — without weakening the substituter path, which still self-heals a genuinely missing NAR from upstream.

## Requirements
### Requirement: The /upload path MUST treat a NAR with a durable bytes-stored marker as present

On the upload-only (`/upload`) path, the system SHALL consider a narinfo's backing NAR present when a `nar_file` row matching the NAR's hash and query has a non-null `bytes_stored_at` marker, **independently of the local filesystem** and **independently of compression**. `PutNar` SHALL set `bytes_stored_at` once it has durably written the NAR's bytes. This lets a reference check on one replica trust that a peer replica (writing to shared storage and the shared database) has stored the NAR, even before this replica's local `stat` observes the write — so a concurrent `nix copy --to .../upload` is not aborted.

The marker SHALL be trusted only on the upload-only path. On the substituter path a genuinely missing NAR SHALL still be self-healed from upstream rather than reported present.

#### Scenario: A peer-stored NAR is present on /upload before local bytes are visible

- **GIVEN** a `nar_file` for hash `H` with `bytes_stored_at` set and NO bytes in this replica's local store
- **AND** a narinfo linked to it
- **WHEN** the narinfo for `H` is read on the upload-only path
- **THEN** the system SHALL return the narinfo (the NAR is present)
- **AND** SHALL NOT report a cache miss / purge

#### Scenario: Presence is compression-agnostic

- **GIVEN** a narinfo advertising `url=nar/<H>.nar` (Compression none)
- **AND** the only backing `nar_file` is `compression=xz` with `bytes_stored_at` set (no `none` row)
- **WHEN** the narinfo for `H` is read on the upload-only path
- **THEN** the system SHALL match the xz `nar_file` by hash+query and report the NAR present

#### Scenario: A byte-less placeholder is NOT trusted

- **GIVEN** a `nar_file` for hash `H` with `bytes_stored_at` NULL and no local bytes
- **WHEN** the narinfo for `H` is read on the upload-only path
- **THEN** the system SHALL report the missing-NAR cache miss (no phantom revival)

#### Scenario: The substituter path still self-heals a missing NAR

- **GIVEN** a `nar_file` for hash `H` with `bytes_stored_at` set but no local bytes
- **WHEN** the narinfo for `H` is read on the substituter (non-upload) path
- **THEN** the marker SHALL NOT be trusted as proof of local servability
- **AND** the system SHALL fall back to upstream self-healing

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

