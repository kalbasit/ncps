## Why

After a CDC→whole-file migration fully drains, `fsck` can no longer reclaim leftover chunk residue. `fsck` only runs chunk checks when it detects "CDC mode," currently `cdc_enabled == "true"` (DB config) **OR** any `nar_file.total_chunks > 0`. Once drain completes, the serve restart deletes the `cdc_enabled` config key **and** every NAR is de-chunked (`total_chunks = 0`) — so both signals go false while the `chunks` table and chunk storage may still hold orphaned data (by definition orphaned chunks are referenced by no `nar_file`). A subsequent `fsck` detects `cdcMode = false`, never initializes the chunk store, and silently skips orphaned-chunk reclamation, stranding those rows and files permanently.

This is not hypothetical: on prod (2026-06-07) ~5.5M orphaned chunk rows were reclaimable only because the one `fsck` run happened to start ~46s before the drain cleared the config. The only current recovery is manually re-inserting `cdc_enabled=true`.

## What Changes

- Add a third CDC-mode detection signal in `fsck`: if chunk residue exists (the `chunks` table is non-empty), enable `cdcMode` so the chunk store is initialized and orphaned-chunk detection/reclamation runs — even when `cdc_enabled` is absent and no `nar_file` references chunks.
- Emit a distinct, informative log when CDC mode is enabled solely by residue detection (vs. active CDC or chunked `nar_file`s), so operators understand why a post-drain `fsck` is doing chunk work.
- Preserve all existing gating: reclamation still only mutates under `--repair`; dry-run continues to report-only.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `fsck`: the CDC-mode detection requirement is extended so a non-empty `chunks` table independently enables CDC-mode checks. This makes orphaned chunk residue detectable and reclaimable across the full CDC lifecycle, including after CDC and drain mode are fully disabled.

## Impact

- **Code**: `pkg/ncps/fsck.go` — the `cdcMode` detection block (currently config-key + `total_chunks>0` fallback). Adds one indexed existence query (`Chunk.Query().Exist(ctx)`).
- **Behavior**: a post-drain `fsck` now initializes the chunk store and reclaims orphaned chunks under `--repair`. No change to `serve`, drain, or migration semantics; CDC config is not recreated.
- **I/O / memory**: one extra cheap existence query per `fsck` run. When residue is found, `fsck` performs the existing chunk-store walk/reclamation it already does in CDC mode — no new per-chunk cost beyond enabling the established path. Negligible network impact.

## Non-goals

- No new CLI flags or config keys; detection is automatic.
- Does not alter how chunks are created, served, or migrated.
- Does not re-enable CDC writes or recreate the deleted `cdc_enabled` config.
- Does not change the reclamation/cascade logic itself — only when it is reached.
