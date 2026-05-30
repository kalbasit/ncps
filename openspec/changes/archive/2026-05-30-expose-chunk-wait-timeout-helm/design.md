## Context

Commit `9256f27c` added `cache.cdc.chunk-wait-timeout` (`CACHE_CDC_CHUNK_WAIT_TIMEOUT`,
default `30s`), registered in `pkg/ncps/serve.go` as the `cache-cdc-chunk-wait-timeout`
flag and applied via `c.SetChunkWaitTimeout(cmd.Duration(...))`. The Helm chart renders
the rest of the `cdc:` block from `config.cdc.*` values but has no entry for this flag, so
operators running the exact deployments that motivated the flag (HA + CDC + slow/shared
storage) cannot tune it without hand-editing rendered config.

The chart already establishes a clear, repeatable pattern for optional `cdc.*` knobs:
- `values.yaml` carries the value (with a `null` default for "use the binary default").
- `templates/configmap.yaml` wraps each key in `{{- if .Values.config.cdc.<x> }}` and
  renders duration/string values with `| quote`.

This is a chart-and-docs-only change; the Go flag already exists.

## Goals / Non-Goals

**Goals:**
- Expose `config.cdc.chunkWaitTimeout` in the chart, rendering `chunk-wait-timeout` into the
  `cache.cdc` block only when set, as a quoted duration.
- Default the value to `null` so existing renders are byte-for-byte unchanged and the binary's
  `30s` default continues to apply.
- Cover the behavior with a Helm unit test (set → rendered; unset → omitted).
- Document the knob at the Helm-chart level and the underlying flag at the CLI/CDC level.

**Non-Goals:**
- Changing the flag's default or runtime behavior.
- Exposing any other unexposed flags or restructuring the `cdc` value layout.
- Adding gateway-timeout coordination logic.

## Decisions

### D1: Render conditionally with `| quote`, mirroring `deleteDelay`

Follow the established pattern at `configmap.yaml:46-48`. The value is a Go duration string
(e.g. `60s`, `1m30s`), so it must be quoted to stay a YAML string:

```yaml
{{- if .Values.config.cdc.chunkWaitTimeout }}
chunk-wait-timeout: {{ .Values.config.cdc.chunkWaitTimeout | quote }}
{{- end }}
```

Placed inside the existing `{{- if .Values.config.cdc.enabled }}` guard, after
`lazy-cleanup-schedule`.

**Alternative considered:** always render with a chart-side default of `"30s"`. Rejected —
it would duplicate the binary's default in two places (drift risk) and change existing
rendered output for current users. `null`-default + conditional keeps a single source of truth.

### D2: `null` default in `values.yaml`

Matches `backgroundWorkers` (`null`, "server default"). Documented in `values.yaml` with the
server default (`30s`) and the gateway-alignment use case noted.

### D3: Helm unit test under `charts/ncps/tests/`

Add assertions in the existing CDC configmap test suite (or a new test file consistent with the
suite's conventions): one asserting `chunk-wait-timeout: "<v>"` is present when the value is set,
one asserting the key is absent when it is null. TDD: write/extend the test first (RED), then add
the template line (GREEN).

### D4: Documentation placement

- Helm: add a row to the `config.cdc.*` table in
  `Installation/Helm Chart/Chart Reference.md` and to the `cdc:` example in `Helm Chart.md`.
- CLI/CDC: add a row to the `--cache-cdc-*` tables in `Configuration/Reference.md` and
  `Features/CDC.md` for `--cache-cdc-chunk-wait-timeout` (`CACHE_CDC_CHUNK_WAIT_TIMEOUT`, `30s`).

## Risks / Trade-offs

- **[Quoting omitted → YAML parses duration oddly]** → Use `| quote` exactly as the sibling
  duration keys do; the unit test asserts the quoted form.
- **[Drift between chart default and binary default]** → Avoided by D1/D2: chart omits the key
  when unset; the binary owns the single `30s` default.
- **[Docs tables drift further behind flags]** → This change also documents the flag the HEAD
  commit left undocumented, reducing existing drift rather than adding to it.

## Migration Plan

Additive, non-breaking. Operators on existing values get identical rendered output. No rollback
concerns; reverting the chart change simply removes the value (binary default resumes).

## Open Questions

None.
