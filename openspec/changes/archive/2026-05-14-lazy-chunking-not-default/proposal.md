## Why

Lazy chunking was made the default in PR #1099, but this is a breaking behavior change for existing deployments: users who haven't opted in now get background chunking, delayed compressed NAR deletion, and additional cron jobs running without their knowledge. An opt-in feature should not silently activate on upgrade.

## What Changes

- **BREAKING** (revert): `--cache-cdc-lazy-chunking-enabled` CLI flag default reverted from `true` → `false`
- Helm `values.yaml`: `lazyChunkingEnabled` default reverted from `true` → `false`
- CLI usage string corrected: "(default: true)" → "(default: false)"
- Helm chart test updated: "should default CDC lazy chunking to true" → "should default CDC lazy chunking to false"
- Helm chart docs/reference updated to reflect `false` default

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None — this is a default-value correction, not a requirement change. No spec-level behavior changes; the capability exists and works identically, it just no longer activates unless explicitly enabled.

## Impact

- **`pkg/ncps/serve.go`**: Change `Value: true` → `Value: false` and fix usage string
- **`charts/ncps/values.yaml`**: Change `lazyChunkingEnabled: true` → `false` and fix comment
- **`charts/ncps/tests/configmap_test.yaml`**: Fix "should default CDC lazy chunking to true" test assertion
- **`docs/`**: Update any docs/chart-reference that state the default is `true`
- No I/O, latency, or memory impact — lazy chunking simply won't run unless opted in

**Non-goals**

- Not removing lazy chunking or any related flags
- Not changing how lazy chunking works when enabled
- Not adding migration tooling for users who were relying on the previous default
