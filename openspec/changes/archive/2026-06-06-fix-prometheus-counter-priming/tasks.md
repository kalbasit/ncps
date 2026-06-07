## 1. Failing tests (TDD red)

- [x] 1.1 In `pkg/cache`, add a test that installs a `sdkmetric.MeterProvider` with a manual/Prometheus reader via `otel.SetMeterProvider`, calls `cache.PrimeMetrics(ctx)`, gathers metrics, and asserts `ncps_nar_served_total` and `ncps_narinfo_served_total` (and the other cache counters) are present at value `0`. Confirm it fails (function undefined / series absent).
- [x] 1.2 Add an analogous prime+scrape test in `pkg/lock` and `pkg/ncps` asserting each package's counters export at `0` after priming.
- [x] 1.3 Add a semantics test: after priming, record one real increment and assert the counter total is `1` (prime did not inflate).
- [x] 1.4 Add a no-op test: calling `PrimeMetrics(context.Background())` with no meter provider installed neither errors nor panics.

## 2. Implementation (TDD green)

- [x] 2.1 Add exported `func PrimeMetrics(ctx context.Context)` to `pkg/cache` that calls `Add(ctx, 0)` on every package-level `Int64Counter` (`narServedCount`, `narInfoServedCount`, `lruCleanupRunsTotal`, `lruNarInfosEvictedTotal`, `lruNarFilesEvictedTotal`, `lruChunksEvictedTotal`, `lruBytesFreedTotal`, `backgroundMigrationObjectsTotal`, `downloadCoordinationFallbackTotal`).
- [x] 2.2 Add `PrimeMetrics(ctx)` to `pkg/lock` priming its counters.
- [x] 2.3 Add `PrimeMetrics(ctx)` to `pkg/ncps` priming its counters (or fold the ncps-package counters in directly at the serve call site if cleaner).
- [x] 2.4 In `pkg/ncps/serve.go`, after the Prometheus/OTel metrics setup block (once the global meter provider is installed), invoke `cache.PrimeMetrics(ctx)`, `lock.PrimeMetrics(ctx)`, and the ncps priming, guarded to run whenever a metrics exporter was configured.

## 3. Verification (TDD refactor + gates)

- [x] 3.1 Run `task test` (race detector) — all new and existing tests pass.
- [x] 3.2 Manually (or via an integration-style test) start ncps with `--prometheus-enabled`, scrape `/metrics` with no traffic, and confirm `ncps_nar_served_total` / `ncps_narinfo_served_total` show `0`.
- [x] 3.3 Run `task fmt` and `task lint` — both exit `0` (each new `//nolint`, if any, carries an explanatory comment).
- [x] 3.4 Cross-check the priming list against the docs (`docs/.../Monitoring.md`, `Observability.md`, `Cache Management.md`); update docs only if any listed counter is intentionally not primed.
