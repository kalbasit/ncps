## 1. CLI Flag Default

- [x] 1.1 In `pkg/ncps/serve.go`: change `Value: true` → `Value: false` for `cache-cdc-lazy-chunking-enabled`
- [x] 1.2 In `pkg/ncps/serve.go`: fix usage string from "(default: true)" → "(default: false)"

## 2. Helm Chart

- [x] 2.1 In `charts/ncps/values.yaml`: change `lazyChunkingEnabled: true` → `false` and fix the comment from "(default: true)" → "(default: false)"
- [x] 2.2 In `charts/ncps/tests/configmap_test.yaml`: fix "should default CDC lazy chunking to true" test — change description and expected pattern to `false`

## 3. Documentation

- [x] 3.1 In `docs/docs/User Guide/Configuration/Reference.md`: change default column for `--cache-cdc-lazy-chunking-enabled` from `true` → `false`
- [x] 3.2 In `docs/docs/User Guide/Features/CDC.md`: change default column from `true` → `false`; update example config to show `lazy-chunking-enabled: false` (or remove from default example)
- [x] 3.3 In `docs/docs/User Guide/Installation/Helm Chart/Chart Reference.md`: change default for `config.cdc.lazyChunkingEnabled` from `true` → `false`
- [x] 3.4 In `docs/docs/User Guide/Installation/Helm Chart.md`: fix inline comment and example value from `true` → `false`

## 4. Verify

- [x] 4.1 Run `helm unittest charts/ncps` and confirm all tests pass
- [x] 4.2 Run `go build .` to confirm no compilation errors
- [x] 4.3 Run `golangci-lint run` and confirm no new lint issues
