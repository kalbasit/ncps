## 1. Helm chart: tests first (RED)

- [x] 1.1 In `charts/ncps/tests/configmap_test.yaml`, add a test asserting that when
  `config.cdc.enabled=true` and `config.cdc.chunkWaitTimeout` is set (e.g. `"60s"`), the
  rendered ConfigMap contains `chunk-wait-timeout: "60s"` in the `cache.cdc` block.
- [x] 1.2 Add a test asserting that when `config.cdc.enabled=true` and `chunkWaitTimeout` is
  left at its `null` default, no `chunk-wait-timeout` key is rendered.
- [x] 1.3 Run `helm unittest charts/ncps` and confirm the new assertions FAIL (RED).

## 2. Helm chart: implementation (GREEN)

- [x] 2.1 Add `chunkWaitTimeout: null` to the `config.cdc` block in `charts/ncps/values.yaml`,
  with a comment documenting the `30s` server default and the gateway-alignment use case.
- [x] 2.2 In `charts/ncps/templates/configmap.yaml`, after `lazy-cleanup-schedule`, add the
  conditional render: `{{- if .Values.config.cdc.chunkWaitTimeout }}` →
  `chunk-wait-timeout: {{ .Values.config.cdc.chunkWaitTimeout | quote }}` → `{{- end }}`.
- [x] 2.3 Run `helm unittest charts/ncps` and confirm all assertions PASS (GREEN).

## 3. Documentation

- [x] 3.1 Add a `config.cdc.chunkWaitTimeout` row to the `config.cdc.*` table in
  `docs/docs/User Guide/Installation/Helm Chart/Chart Reference.md`.
- [x] 3.2 Add `chunkWaitTimeout` to the `cdc:` example block in
  `docs/docs/User Guide/Installation/Helm Chart.md`.
- [x] 3.3 Add a `--cache-cdc-chunk-wait-timeout` row (`CACHE_CDC_CHUNK_WAIT_TIMEOUT`, default
  `30s`) to the CDC flags table in `docs/docs/User Guide/Configuration/Reference.md`.
- [x] 3.4 Add the same flag row to the flags table in
  `docs/docs/User Guide/Features/CDC.md`.

## 4. Verify

- [x] 4.1 Run `task fmt` and confirm it exits clean.
- [x] 4.2 Run `helm unittest charts/ncps` (and `nix flake check` if feasible) and confirm green.
