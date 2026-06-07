# metrics-exposure Specification

## Purpose

Define how ncps exposes its OpenTelemetry metrics so that monitoring systems can
observe the proxy's behavior. In particular, this capability governs the
availability and initial state of metric instruments in the `GET /metrics`
Prometheus exposition.

## Requirements

### Requirement: Counter metrics are exposed from startup

All OpenTelemetry counter instruments that ncps defines (e.g. `ncps_nar_served_total`, `ncps_narinfo_served_total`, the LRU eviction counters, the background-migration counter, and the download-coordination fallback counter) SHALL be present in the `GET /metrics` output as soon as the Prometheus exporter is active, initialized to value `0`, regardless of whether any request, eviction, or migration has occurred yet.

The system SHALL achieve this by recording a single zero-valued measurement (`Add(ctx, 0)`) on each counter after the global meter provider has been installed. Priming MUST NOT alter counting semantics: the first real event still advances the counter from `0` to `1`.

Observable gauges and histograms are out of scope for this requirement: gauges already export via their registered callbacks, and histograms remain absent until their first genuine observation.

#### Scenario: Documented counters present on an idle instance

- **WHEN** ncps starts with Prometheus enabled and `GET /metrics` is scraped before any NAR or narinfo has been served
- **THEN** the response includes `ncps_nar_served_total` and `ncps_narinfo_served_total` time series, each with value `0`

#### Scenario: Priming preserves counting semantics

- **WHEN** a counter has been primed to `0` at startup and ncps then serves exactly one NAR
- **THEN** `ncps_nar_served_total` reports a total of `1`, not `2` (the zero-valued prime does not inflate the count)

#### Scenario: All package counters are primed

- **WHEN** ncps starts with Prometheus enabled and `GET /metrics` is scraped with no traffic
- **THEN** every counter instrument defined in `pkg/cache`, `pkg/lock`, and `pkg/ncps` appears at value `0`

#### Scenario: Priming is a no-op when metrics are disabled

- **WHEN** ncps starts with Prometheus (and OTLP) metrics disabled
- **THEN** priming the counters causes no error and no panic, and the process starts normally
