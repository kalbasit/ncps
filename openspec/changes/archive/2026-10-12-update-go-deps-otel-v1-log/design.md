## Context

`otel/log/global` now forwards to the root `otel` package, which owns the global logger provider in
v1.47. Both functions keep the same signatures and semantics.

## Goals / Non-Goals

**Goals:** current dependencies for v0.10.0, with lint clean and no behaviour change.

**Non-Goals:** reworking the telemetry setup.

## Decisions

- One combined change instead of six PRs: they overlap in `go.mod`/`go.sum` and share the same
  lint fix and `vendorHash`. Renovate closes its PRs once `main` already has these versions.
- Rename the calls instead of suppressing SA1019: the replacement is a drop-in with the same
  behaviour.

## Risks / Trade-offs

- Telemetry wiring is covered by the existing `pkg/otel` and `pkg/otelzerolog` tests and the full
  suite. `nix build .#ncps` verifies the vendored modules.
