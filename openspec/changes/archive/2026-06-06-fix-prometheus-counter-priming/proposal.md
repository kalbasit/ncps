## Why

GitHub issue #1337: users scraping `/metrics` on a freshly-started or idle ncps instance do not see documented counters such as `ncps_nar_served_total` and `ncps_narinfo_served_total`. The OpenTelemetry Prometheus exporter only emits a counter time series after the counter records its first measurement, so until a NAR/narinfo is actually served (or an eviction/migration fires) these series are simply absent — contradicting the documentation that promises them and breaking dashboards/alerts that reference them from the start.

## What Changes

- Prime every OpenTelemetry `Int64Counter` instrument (in `pkg/cache`, `pkg/lock`, and `pkg/ncps`) with a zero-valued `Add(ctx, 0)` once the global meter provider is installed, so each documented counter appears in `/metrics` at value `0` from startup.
- Each metrics-owning package exposes a small exported priming function; `pkg/ncps/serve.go` invokes them after metrics exporters are configured.
- No new metrics, no renames, no behavioral change to counting semantics — only earlier visibility of already-defined counters.

## Capabilities

### New Capabilities
- `metrics-exposure`: Defines that all documented counter metrics are exposed at `GET /metrics` from process startup (initialized to zero), independent of whether any traffic has been served yet.

### Modified Capabilities
<!-- None: no existing spec's requirements change. -->

## Impact

- **Code**: `pkg/cache`, `pkg/lock`, `pkg/ncps` (new priming functions + a call site in `serve.go`). Observable gauges and histograms are unaffected (gauges already export via callbacks).
- **APIs**: `/metrics` output gains zero-valued counter series at startup; no schema/endpoint change.
- **I/O / network / memory**: Negligible — a handful of one-time `Add(ctx, 0)` calls at boot; no steady-state overhead.

## Non-goals

- Not adding or renaming any metric.
- Not changing histogram or observable-gauge exposure behavior.
- Not reconciling the OTLP-vs-Prometheus dual-meter-provider interaction (separate concern).
