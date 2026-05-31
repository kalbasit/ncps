## 1. Config Layer — Revert DeleteCDCConfig on disable

- [x] 1.1 In `pkg/config/config.go`: remove the `DeleteCDCConfig` call from `validateCDCConfig` for the `storedEnabled && !enabled` case — return nil without touching the stored keys
- [x] 1.2 Update `ValidateOrStoreCDCConfig` doc comment to reflect the new behavior: enabled→disabled returns nil and leaves stored config intact
- [x] 1.3 Update `pkg/config/config_test.go` test `testValidateCDCDisableAfterEnabled`: assert nil return AND that all four CDC config keys are still present in the database (opposite of what PR #1304 asserted)

## 2. Cache Layer — Split write gate from read gate

- [x] 2.1 In `pkg/cache/cache.go`: add `isChunkStoreAvailable() bool { return c.chunkStore != nil }` (with `cdcMu.RLock`)
- [x] 2.2 Change `isServable`: replace the `!c.isCDCEnabled()` guard with `!c.isChunkStoreAvailable()`
- [x] 2.3 Change `HasNarInChunks`: replace the `!c.isCDCEnabled()` guard with `!c.isChunkStoreAvailable()`
- [x] 2.4 Change `GetNarInfo` normalization: replace the `c.isCDCEnabled()` guard with `c.isChunkStoreAvailable()`
- [x] 2.5 Write a unit test that verifies: with `cdcEnabled=false` and a non-nil chunk store (drain mode), `isServable` returns true for a `nar_file` with `total_chunks > 0`

## 3. Serve Layer — Initialize chunk store in drain mode with auto-complete detection

- [x] 3.1 In `serve.go` `createCache`, before calling `ValidateOrStoreCDCConfig`, read `cfg.GetCDCEnabled(ctx)` to capture the stored value (`storedWasEnabled`)
- [x] 3.2 After `ValidateOrStoreCDCConfig` returns nil, detect drain mode: `storedWasEnabled=true && !cdcEnabled`
- [x] 3.3 In drain mode: query `dbClient.Ent().NarFile.Query().Where(entnarfile.TotalChunksGT(0)).Count(ctx)` to get the remaining chunk count
- [x] 3.4 If drain count == 0: call `cfg.DeleteCDCConfig(ctx)`, log "CDC drain complete, stored config cleared", and skip chunk store initialization (fully disabled)
- [x] 3.5 If drain count > 0: call `getChunkStorageBackend` and `c.SetChunkStore` WITHOUT `SetCDCConfiguration(true, ...)` and without starting lazy-chunking; log warning with the chunk count
- [x] 3.6 Ensure the existing `if cdcEnabled { ... c.SetChunkStore(...) }` block still runs for full CDC-enabled mode (no regression)

## 4. migrate-chunks-to-nar — Use chunk count, not DB flag

- [x] 4.1 In `pkg/ncps/migrate_chunks_to_nar.go`: remove the early check that fails when `cdc_enabled != "true"` in the DB
- [x] 4.2 Replace it with a count query: `dbClient.Ent().NarFile.Query().Where(entnarfile.TotalChunksGT(0)).Count(ctx)`; if count == 0, log "nothing to migrate" and return nil
- [x] 4.3 Update any test that expected the "CDC was never enabled" error path to instead expect success-with-zero-work

## 5. Verification

- [x] 5.1 Run `task fmt && task lint && task test` and confirm all pass
