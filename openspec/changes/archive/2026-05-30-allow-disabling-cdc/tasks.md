## 1. Config Layer

- [x] 1.1 In `pkg/config/config.go`: add a `DeleteCDCConfig(ctx)` helper that deletes all four CDC config keys (`cdc_enabled`, `cdc_min`, `cdc_avg`, `cdc_max`) using the existing `deleteConfig` pattern (or equivalent)
- [x] 1.2 In `validateCDCConfig`: remove the `if storedEnabled && !enabled { return ErrCDCDisabledAfterEnabled }` block; replace with a call to `DeleteCDCConfig` and return nil for the enabled→disabled transition
- [x] 1.3 Remove `ErrCDCDisabledAfterEnabled` sentinel error (definition and any remaining references)
- [x] 1.4 Write unit tests in `pkg/config/config_test.go` for the enabled→disabled transition: validate that `ValidateOrStoreCDCConfig(ctx, false, ...)` returns nil and the four stored keys are absent afterward

## 2. Serve Layer Warning

- [x] 2.1 In `pkg/ncps/serve.go`: after `ValidateOrStoreCDCConfig` returns nil, detect the CDC-disable transition (previous stored enabled=true, current enabled=false) by checking the count of `nar_file` rows with `total_chunks > 0` using `dbClient.Ent().NarFile.Query().Where(entnarfile.TotalChunksGT(0)).Count(ctx)`
- [x] 2.2 Log a structured `Warn`-level log entry when the count > 0, including the chunk count and a reference to `migrate-chunks-to-nar`; log nothing (or `Debug`) when count = 0

## 3. Test Cleanup

- [x] 3.1 Update any existing tests in `pkg/config/config_test.go` that assert `ErrCDCDisabledAfterEnabled` is returned — flip them to assert nil is returned and stored keys are cleared
- [x] 3.2 Update any existing tests in `pkg/ncps/` that reference `ErrCDCDisabledAfterEnabled`

## 4. Verification

- [x] 4.1 Run `task fmt && task lint && task test` and confirm all pass
