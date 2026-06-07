## Context

Counter instruments are created in package `init()` functions via the global OTel meter (`otel.Meter(...)`) in `pkg/cache/cache.go`, `pkg/lock/metrics.go`, and `pkg/ncps/metrics.go`. The global meter returns *delegating* instruments before `otel.SetMeterProvider` is called; once the provider is installed (by `prometheus.SetupPrometheusMetrics` and/or `otel.SetupOTelSDK` in `pkg/ncps/serve.go`), those instruments forward to the real SDK instruments.

The OTel→Prometheus exporter materializes a counter series only after the counter records at least one measurement. Histograms behave the same; observable gauges export via callbacks and are unaffected. The counters in question are only incremented on real events:
- `narServedCount` (`cache.go:1078`), `narInfoServedCount` (`cache.go:3559`)
- LRU eviction counters, `backgroundMigrationObjectsTotal`, `downloadCoordinationFallbackTotal`
- counters in `pkg/lock` and `pkg/ncps`

So an idle/fresh instance scraping `/metrics` sees none of them — the bug in #1337.

## Goals / Non-Goals

**Goals:**
- Documented counters appear at `/metrics` with value `0` from startup.
- Counting semantics unchanged (zero-add must not inflate totals).
- Minimal, localized change; no new dependencies.

**Non-Goals:**
- Priming histograms (a synthetic observation would skew distributions) or gauges (already exported).
- Adding/renaming metrics or changing attribute sets.
- Resolving the dual meter-provider overwrite when both OTLP and Prometheus are enabled.

## Decisions

### Decision 1: Prime counters with `Add(ctx, 0)` rather than restructure instrument creation

`Add(ctx, 0)` is the idiomatic OTel way to force a counter series to exist at zero. Alternatives considered:
- *Per-handler lazy priming*: scatters logic and still misses untouched code paths — rejected.
- *Switch counters to ObservableCounters with callbacks*: large refactor, changes the increment model everywhere — rejected.
- *Custom Prometheus collector*: bypasses OTel, duplicates definitions — rejected.

### Decision 2: One exported priming function per metrics-owning package

Each of `pkg/cache`, `pkg/lock`, `pkg/ncps` exposes an exported function (e.g. `PrimeMetrics(ctx context.Context)`) that calls `Add(ctx, 0)` on each of its package-level counters. This keeps each package the owner of its own instrument list (no cross-package globals) and is trivially unit-testable.

Signature (per package):
```go
// PrimeMetrics records a zero-valued measurement on every counter so the
// series are exported from startup. Safe to call once after the global
// meter provider is installed; a no-op effect on counting semantics.
func PrimeMetrics(ctx context.Context)
```

### Decision 3: Call priming after the meter provider is installed, in `serve.go`

Priming MUST run after `otel.SetMeterProvider` (inside `SetupPrometheusMetrics`/`SetupOTelSDK`); measurements recorded on the delegating instruments before a provider is set are dropped. Call sites go in `pkg/ncps/serve.go` immediately after the metrics-setup block, guarded so they run whenever a metrics exporter was configured. Calling with no provider installed is harmless (drops the zero-add), satisfying the "no-op when disabled" scenario.

### Decision 4: Prime with the empty attribute set

Real increments may carry attributes (e.g. per-cache). Priming emits a single attribute-less `{}` series at `0`. This is standard and harmless: PromQL aggregations (`rate(ncps_nar_served_total[5m])`) span all series and the `{}` series stays at `0`. Mirroring dynamic attributes at startup is impossible (values unknown) and unnecessary.

## Risks / Trade-offs

- [Extra `{}` zero series alongside attributed series] → Cosmetic; documented, standard OTel behavior; aggregations unaffected.
- [Priming runs before provider set → silently dropped] → Mitigated by ordering the call after `SetupPrometheusMetrics` in `serve.go`; covered by the "no-op when disabled" scenario and an integration scrape test.
- [Future counters added without priming] → Mitigated by colocating each package's prime list with its instrument declarations and a unit test that scrapes for zero series.

## Migration Plan

Pure additive code change; no DB migration, no config, no API change. Deploy normally. Rollback is a plain revert — counters simply return to appearing only after first use.

## Open Questions

- None blocking. (Out of scope: whether enabling both OTLP and Prometheus should compose meter providers instead of the second overwriting the first.)
