## Why

Commit `9256f27c` added the operator-configurable flag `cache.cdc.chunk-wait-timeout`
(`CACHE_CDC_CHUNK_WAIT_TIMEOUT`, default `30s`) so the per-chunk wait in progressive
CDC streaming can be aligned with a deployment's reverse-proxy/gateway timeout, avoiding
spurious 504s on high-latency storage. The flag is registered in the binary but is **not
exposed by the Helm chart**, so the very operators who hit this failure mode (HA,
CDC-enabled, shared/slow storage) cannot tune it without hand-editing the rendered config.

## What Changes

- Add a `config.cdc.chunkWaitTimeout` value to `charts/ncps/values.yaml`, documented and
  defaulted to `null` (chart omits the key → binary applies its own `30s` default), matching
  how the other optional `cdc.*` knobs are handled.
- Render `chunk-wait-timeout` into the `cdc:` block of `charts/ncps/templates/configmap.yaml`
  only when the value is set, mirroring the existing conditional rendering of `deleteDelay`,
  `lazyRecoverySchedule`, etc. (quoted, since it is a Go duration string).
- Add a Helm unit test under `charts/ncps/tests/` asserting the key is rendered when set and
  omitted when null.
- Document the new knob in `docs/docs/`: add `config.cdc.chunkWaitTimeout` to the Helm
  `Chart Reference.md` value table and the `Helm Chart.md` `cdc:` example, **and** document the
  underlying `--cache-cdc-chunk-wait-timeout` / `CACHE_CDC_CHUNK_WAIT_TIMEOUT` flag in
  `Configuration/Reference.md` and `Features/CDC.md` (the HEAD commit added the flag without
  updating these tables).

No application/Go code changes; the flag already exists.

## Non-goals

- Changing the flag's default value or its behavior in the Go binary.
- Exposing any other unexposed flags, or restructuring the chart's `cdc` value layout.
- Adding HA/gateway-timeout coordination logic; this only surfaces an existing knob.

## Capabilities

### New Capabilities

- `helm-cdc-chunk-wait-timeout`: The Helm chart MUST allow operators to configure the CDC
  per-chunk wait timeout via `config.cdc.chunkWaitTimeout`, rendering it into the cache CDC
  config when set and omitting it (binary default applies) when unset.

### Modified Capabilities

<!-- none — this surfaces an existing flag through the chart; no existing spec's requirements change -->

## Impact

- **Code/config**: `charts/ncps/values.yaml`, `charts/ncps/templates/configmap.yaml`, new
  test under `charts/ncps/tests/`. No Go, database, or migration impact.
- **Docs**: `docs/docs/User Guide/Installation/Helm Chart/Chart Reference.md`,
  `.../Helm Chart.md`, `.../Configuration/Reference.md`, `.../Features/CDC.md`.
- **I/O / latency / memory**: None at the chart-render level. At runtime the value only bounds
  an existing per-chunk wait; leaving it unset preserves today's `30s` behavior exactly.
- **Validation**: `helm unittest charts/ncps` (and `nix flake check`).
