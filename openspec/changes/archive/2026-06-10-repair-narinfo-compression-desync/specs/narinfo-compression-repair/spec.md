## ADDED Requirements

### Requirement: Repair narinfos advertising a non-producible compression

The system SHALL, during `fsck --repair`, rewrite any narinfo that advertises a non-producible compression (one ncps has no compressor for, e.g. `xz`) whose backing NAR is stored under a different compression, to the servable uncompressed form: `URL: nar/<nar_hash>.nar`, `Compression: none`, with `FileHash` and `FileSize` cleared. It MUST NOT modify a narinfo whose advertised compression matches a stored representation or is directly producible, and the repair MUST be idempotent.

#### Scenario: xz-advertised narinfo backed by a non-xz NAR is rewritten to none

- **WHEN** a narinfo advertises `Compression: xz` (`URL: nar/<file_hash>.nar.xz`) but the backing NAR is stored as `none`/`zstd`/chunks (no `xz` nar_file) and `fsck --repair` runs
- **THEN** the narinfo is rewritten to `URL: nar/<nar_hash>.nar`, `Compression: none`, with `FileHash`/`FileSize` cleared, so it is served by transparent decompression

#### Scenario: healthy narinfo is left untouched

- **WHEN** a narinfo advertises a compression that matches a stored representation (e.g. `xz` with an `xz` nar_file, or `none`/`zstd`) and `fsck --repair` runs
- **THEN** the narinfo is not modified

#### Scenario: repair is idempotent

- **WHEN** `fsck --repair` runs a second time after a prior repair
- **THEN** no further narinfos are rewritten
