## Why

CDC (content-defined chunking) is currently a one-way door: once enabled, the server hard-fails at startup if you try to turn it off, because chunked NARs would be unservable. Now that `migrate-chunks-to-nar` (PR #1301) exists as an explicit migration path to reconstruct whole NARs from chunks, the guard is no longer necessary — operators can run the migration first and then safely disable CDC.

## What Changes

- Remove the hard startup error that blocks disabling CDC after it was previously enabled.
- Replace it with a warning that logs how many chunked NARs remain, nudging operators to run `migrate-chunks-to-nar` first if they haven't.
- If zero chunked NARs remain (all migrated), allow the transition silently.
- Clear the `cdc_enabled` stored config key when CDC is disabled, so a future re-enable is treated as a fresh start.

## Capabilities

### New Capabilities

- `cdc-disable`: Operators can transition a deployment from CDC-enabled to CDC-disabled by first running `migrate-chunks-to-nar`, then setting `cdc.enabled: false`. The server warns (not errors) if un-migrated chunked NARs are detected but starts successfully.

### Modified Capabilities

- `cdc-chunking`: The startup validation requirement changes — disabling CDC after enabling it is now allowed (with a warning), not forbidden.

## Impact

- `pkg/config/config.go`: Remove `ErrCDCDisabledAfterEnabled` guard; add `DeleteCDCConfig` helper.
- `pkg/ncps/serve.go`: Add chunked-NAR count warning when CDC is disabled but chunks remain.
- `pkg/cache/`: Any code relying on the assumption that `cdc_enabled` stored = current must handle the false→false transition.
- No API surface changes. No migration required. No performance impact.
- **Non-goals**: Automatically running `migrate-chunks-to-nar` at startup. Blocking startup when un-migrated chunks exist. Any changes to the CDC chunking write path.
