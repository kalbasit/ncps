## Why

#1479 added `--cache-storage-stat-timeout`, but only `config.example.yaml` documents it. It is the one
serve flag missing from both the docs and the Helm chart, so chart users cannot tune it to match their
ingress read timeout, which is the knob the fix depends on. The CHANGELOG also has no entry for #1479,
#1528, #1403, #1402 or #1478 ahead of v0.10.0.

## What Changes

- Helm: a new `config.storage.statTimeout` value (default `null`, which uses the server default of 5s)
  rendered as `cache.storage.stat-timeout`. `"0"` is rendered, so the bound can be disabled. Schema
  entry and helm unittests included.
- Docs: rows for the flag in the configuration reference and the chart reference.
- CHANGELOG: entries for #1479/#1528, #1403, #1402 and #1478.

## Non-goals

- Any change to server behaviour or to the flag's default.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

_None._ This is configuration plumbing and documentation for an existing flag, so `skip_specs: true`.

## Impact

- `charts/ncps/{values.yaml,values.schema.json,templates/configmap.yaml,tests/configmap_test.yaml}`,
  `docs/`, `CHANGELOG.md`.
- I/O, network latency, memory: none.
