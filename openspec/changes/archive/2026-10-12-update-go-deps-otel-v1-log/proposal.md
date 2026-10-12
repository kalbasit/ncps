## Why

Six Renovate Go-module PRs (#1520, #1521, #1522, #1524, #1526, #1527) were blocked before v0.10.0.
OpenTelemetry 1.47 promotes the log API to v1 and deprecates `go.opentelemetry.io/otel/log/global`,
so lint fails on our two call sites. The PRs also conflict with each other and each would need a
`vendorHash` refresh. Landing them as one change gets the release onto current dependencies.

## What Changes

- Bump the OpenTelemetry Go monorepo to v1.47.0 (log API v1.47.0, log exporters v0.23.0, Prometheus
  exporter v0.69.0) and otelhttp to v0.72.0.
- Bump goose to v3.29.0, prometheus/client_golang to v1.25.0 and golang.org/x/term to v0.47.0, plus
  the indirect updates they pull in.
- Replace the deprecated `log/global` calls with `otel.SetLoggerProvider` and `otel.GetLoggerProvider`.
- Refresh the Nix `vendorHash`.

## Non-goals

- Any behaviour change: logs, traces and metrics are exported exactly as before.
- Updating dependencies that Renovate has not proposed.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

_None._ This change is a dependency bump with a mechanical API rename, so it sets `skip_specs: true`.

## Impact

- `go.mod`, `go.sum`, `nix/packages/ncps/default.nix`, `pkg/otel/otel.go`, `pkg/otelzerolog/logging.go`.
- I/O, network latency, memory: no expected change.
