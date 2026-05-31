## Context

CDC (content-defined chunking) has a one-way guard in `pkg/config/config.go:validateCDCConfig`: when `storedEnabled=true` (DB) and `enabled=false` (current config), the server hard-fails at startup with `ErrCDCDisabledAfterEnabled`. This was correct before `migrate-chunks-to-nar` existed, because no migration path existed.

PR #1301 added `migrate-chunks-to-nar`, which reconstructs whole NARs from chunks and flips their DB records. Operators can now safely drain the chunked NAR inventory before disabling CDC. The hard startup error is now an unnecessary blocker.

The config validation call site is `pkg/ncps/serve.go:1088`. The `Config` struct (`pkg/config/config.go`) holds only the config store (key/value pairs); it does not have access to the NarFile Ent table. The NarFile count query lives at the `dbClient.Ent()` layer, available in `serve.go` but not in `config.go`.

## Goals / Non-Goals

**Goals:**
- Allow `cdc.enabled: false` to succeed on a cluster that previously had CDC enabled.
- Log a warning at startup if un-migrated chunked NARs remain, nudging operators to run `migrate-chunks-to-nar`.
- Clear stored CDC config keys in the DB when transitioning enabled→disabled, so a future re-enable is treated as a first boot.

**Non-Goals:**
- Automatically running `migrate-chunks-to-nar` at startup.
- Blocking startup when un-migrated chunks exist.
- Serving un-migrated chunked NARs after CDC is disabled (existing cache-miss-recovery handles re-downloading on demand).
- Any changes to the CDC chunking write path.

## Decisions

### Remove the hard error; clear stored keys on transition

**Decision**: In `validateCDCConfig`, remove the `if storedEnabled && !enabled { return ErrCDCDisabledAfterEnabled }` block. Replace it with a call to clear the four stored CDC config keys (`cdc_enabled`, `cdc_min`, `cdc_avg`, `cdc_max`) from the config DB.

**Why**: The error was a safety guard against making chunks unservable. With `migrate-chunks-to-nar`, operators have the tool to drain chunks before disabling. Clearing stored keys gives a clean first-boot state on any future re-enable, avoiding stale size mismatches.

**Alternative considered**: Leave stored keys and just remove the error. Rejected: stale `cdc_enabled=true` in DB would confuse `loadCDCConfigFromDB` on a future restart without the explicit `--cache-cdc-enabled=false` flag.

### Count remaining chunked NARs in `serve.go`, not `config.go`

**Decision**: After `ValidateOrStoreCDCConfig` returns nil for a CDC-disable transition, `serve.go` queries `dbClient.Ent().NarFile.Query().Where(entnarfile.TotalChunksGT(0)).Count(ctx)` and logs a structured warning if the count is > 0.

**Why**: `config.go` has no access to the Ent client. Putting the count in `serve.go` keeps `config.go` as a pure key/value store and avoids a new dependency injection. The warning is a startup UX concern, not a config validation concern.

**Alternative considered**: Add a `CountChunkedNars func(context.Context) (int, error)` hook to `ValidateOrStoreCDCConfig`. Rejected: overcomplicates the config API for a one-time startup message.

### Un-migrated chunks become cache misses, not errors

**Decision**: After CDC is disabled, any `nar_file` row still with `total_chunks > 0` is simply not served by the chunk-serving path (which is skipped when CDC is disabled). Clients requesting those NARs trigger the normal cache-miss-recovery path (upstream re-fetch).

**Why**: This is already how the system behaves for any cache miss. No special handling needed. The warning at startup informs operators; the recovery path handles individual request failures gracefully.

## Risks / Trade-offs

- **Un-migrated chunks served as misses**: Operators who disable CDC without running `migrate-chunks-to-nar` first will see cache misses for previously-cached chunked NARs. Mitigation: structured warning at startup shows the count of remaining chunked NARs.
- **DB state after clear**: If the server crashes between clearing `cdc_enabled` and clearing the size keys, `cdc_enabled` is absent but size keys remain. `loadCDCConfigFromDB` checks `cdc_enabled` first and returns early on `ErrConfigNotFound`, so orphaned size keys are harmless.
- **Helm/operator restart timing**: A rolling restart may have pods with CDC enabled and pods with it disabled simultaneously. The write path is gated on `cdcEnabled`, so newly arriving NARs during the rollout go to whole-file storage. Existing chunked NARs continue to be served by the still-enabled pods until rollout completes.

## Migration Plan

1. Run `ncps migrate-chunks-to-nar` to completion (all chunked NARs converted to whole files).
2. Set `cdc.enabled: false` (or remove the `cdc` block) in the ncps config.
3. Restart the server. Startup logs a warning if any chunks remain (count = 0 means clean).
4. No database migration required; config keys are cleared at runtime.

**Rollback**: To re-enable CDC, set `cdc.enabled: true` with desired chunk sizes. Because stored keys were cleared, the server treats it as a fresh first-boot and stores the new sizes.
