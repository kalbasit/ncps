## 1. Tests first (TDD — red)

- [x] 1.1 Add a failing test asserting `cdcMode` is enabled when `cdc_enabled` is absent, no `nar_file` has `total_chunks > 0`, but the `chunks` table is non-empty (post-drain residue state).
- [x] 1.2 Add a failing test asserting `cdcMode` stays false (no chunk store init, no chunk checks) when `cdc_enabled` is absent, no chunked `nar_file`s, and the `chunks` table is empty (never-CDC cache).
- [x] 1.3 Add a test asserting precedence: when `cdc_enabled == "true"` and chunk rows exist, `cdcMode` is enabled via signal 1 and the residue-detection log is NOT emitted.
- [x] 1.4 Add an integration/e2e test (or extend an existing fsck residue test) covering the full path: post-drain orphaned chunks → `fsck --repair` reclaims orphaned chunk DB rows and storage files; dry-run reports but does not delete.

## 2. Implementation (green)

- [x] 2.1 In `pkg/ncps/fsck.go` "Detect CDC mode" block, add a third signal after the `total_chunks > 0` fallback: when still `!cdcMode`, run `dbClient.Ent().Chunk.Query().Exist(ctx)`; on true set `cdcMode = true`. Handle the query error by warning and leaving `cdcMode` unchanged (consistent with the existing fallback's error handling).
- [x] 2.2 Emit a distinct `Info` log when `cdcMode` is enabled solely by the residue signal (signals 1 and 2 false), with wording that does NOT imply CDC writes were re-enabled and is distinguishable from the existing signal-2 `Warn`.
- [x] 2.3 Confirm no change to the chunk-store init (`getChunkStorageBackend`), `collectFsckSuspects`, or reclaim/cascade paths — the new signal only flips `cdcMode`.

## 3. Verify

- [x] 3.1 Run `task test` (race detector) — all new and existing fsck tests pass.
- [x] 3.2 Run `task lint` and `task fmt` — zero issues, clean formatting.
- [x] 3.3 Manually reason through / spot-check that an active-CDC and a still-chunked-NARs run produce byte-identical behavior and logs to pre-change.

## 4. Spec sync

- [x] 4.1 Verify implementation matches the `fsck` delta spec scenarios (`/opsx:verify`); adjust code or spec wording (D2 log text) if they diverge.
