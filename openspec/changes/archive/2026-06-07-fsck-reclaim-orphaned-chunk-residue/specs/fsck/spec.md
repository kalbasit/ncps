## ADDED Requirements

### Requirement: Fsck MUST enable CDC mode when chunk residue exists, even after CDC and drain are fully disabled

`ncps fsck` SHALL determine `cdcMode` from any of the following signals, in order, treating the first match as sufficient:

1. The DB config key `cdc_enabled` equals `"true"`.
2. At least one `nar_file` has `total_chunks > 0`.
3. The `chunks` table is non-empty (at least one chunk row exists).

Signal 3 is the new residue signal. Orphaned chunks are, by definition, referenced by no `nar_file`, so signals 1 and 2 both go false once CDC is disabled and every NAR has been de-chunked — leaving residue undetectable without signal 3. The residue check SHALL be a single indexed existence query (`Chunk.Query().Exist(ctx)`), not a full scan.

When `cdcMode` is enabled, `ncps fsck` SHALL initialize the chunk store via the existing storage-backend configuration (independent of any CDC config keys) and run the established orphaned-chunk detection and, under `--repair`, reclamation.

When `cdcMode` is enabled **solely** by signal 3 (signals 1 and 2 both false), `ncps fsck` SHALL emit a distinct informational log indicating CDC mode was enabled because chunk residue was detected, so operators understand why a post-drain `fsck` is performing chunk work. This log SHALL be distinguishable from the existing fallback warning emitted when signal 2 triggers without signal 1.

#### Scenario: Post-drain orphaned chunks are detected and reclaimed

- **WHEN** `cdc_enabled` is absent from DB config, no `nar_file` has `total_chunks > 0`, the `chunks` table contains orphaned chunk rows, and `ncps fsck --repair` runs
- **THEN** `cdcMode` is enabled via the residue signal, the chunk store is initialized
- **AND** the orphaned chunk DB rows and their backing storage files are reclaimed
- **AND** a distinct "CDC mode enabled: chunk residue detected" informational log is emitted

#### Scenario: Post-drain dry-run reports residue without deleting

- **WHEN** the same post-drain residue exists and `ncps fsck` runs without `--repair`
- **THEN** `cdcMode` is enabled via the residue signal and the orphaned chunks are reported in the summary
- **AND** no chunk DB rows or storage files are deleted

#### Scenario: A cache that never used CDC stays in non-CDC mode

- **WHEN** `cdc_enabled` is absent, no `nar_file` has `total_chunks > 0`, and the `chunks` table is empty
- **THEN** `cdcMode` remains false, the chunk store is not initialized, and `fsck` performs no chunk checks

#### Scenario: Active CDC takes precedence over the residue signal

- **WHEN** `cdc_enabled` equals `"true"` and chunk rows also exist
- **THEN** `cdcMode` is enabled via signal 1 and the residue-detection log is NOT emitted
