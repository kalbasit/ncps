# Capability Spec: Helm CDC Chunk-Wait Timeout

## Purpose

Defines requirements for exposing the CDC per-chunk wait timeout through the Helm
chart and documenting it across the Helm-chart and binary-flag levels.

## Requirements

### Requirement: Helm chart exposes the CDC chunk-wait timeout

The Helm chart MUST allow operators to configure the CDC per-chunk wait timeout
through a `config.cdc.chunkWaitTimeout` value. When set, the chart MUST render the
corresponding `chunk-wait-timeout` key inside the `cache.cdc` block of the generated
config (the ConfigMap), as a quoted Go duration string. The value MUST be defaulted
to `null` in `values.yaml` so that, when the operator does not set it, the chart omits
the key entirely and the ncps binary applies its own built-in default (`30s`).

#### Scenario: Operator sets chunkWaitTimeout

- **WHEN** the chart is rendered with `config.cdc.enabled=true` and
  `config.cdc.chunkWaitTimeout="60s"`
- **THEN** the rendered ConfigMap's `cache.cdc` block contains
  `chunk-wait-timeout: "60s"`

#### Scenario: Operator leaves chunkWaitTimeout unset

- **WHEN** the chart is rendered with `config.cdc.enabled=true` and
  `config.cdc.chunkWaitTimeout` left at its `null` default
- **THEN** the rendered ConfigMap's `cache.cdc` block contains no `chunk-wait-timeout`
  key, allowing the binary's built-in `30s` default to apply

#### Scenario: CDC disabled

- **WHEN** the chart is rendered with `config.cdc.enabled=false`
- **THEN** no `cdc` block (and therefore no `chunk-wait-timeout` key) is rendered,
  regardless of the `config.cdc.chunkWaitTimeout` value

### Requirement: Documentation covers the chunk-wait timeout knob

The user-facing documentation MUST describe how to configure the CDC chunk-wait
timeout at both the Helm-chart and binary-flag levels. The Helm chart reference and
its example MUST document `config.cdc.chunkWaitTimeout`, and the CLI/CDC references
MUST document the underlying `--cache-cdc-chunk-wait-timeout` flag and its
`CACHE_CDC_CHUNK_WAIT_TIMEOUT` environment variable, including the `30s` default.

#### Scenario: Helm chart reference lists the value

- **WHEN** a reader consults the Helm chart reference and example documentation
- **THEN** `config.cdc.chunkWaitTimeout` is listed with its purpose and default

#### Scenario: CLI reference lists the flag

- **WHEN** a reader consults the configuration reference and the CDC feature docs
- **THEN** `--cache-cdc-chunk-wait-timeout` / `CACHE_CDC_CHUNK_WAIT_TIMEOUT` is listed
  with its purpose and `30s` default
