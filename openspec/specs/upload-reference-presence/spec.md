# upload-reference-presence Specification

## Purpose
TBD - created by archiving change upload-reference-presence-marker. Update Purpose after archive.
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

