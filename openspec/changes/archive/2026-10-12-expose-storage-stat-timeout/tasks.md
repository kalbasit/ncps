## 1. Chart (TDD)

- [x] 1.1 Add helm unittests: value set (local and S3), `"0"` rendered, omitted when null. Confirm RED.
- [x] 1.2 Add `config.storage.statTimeout` to values, schema and the configmap template. Confirm GREEN.

## 2. Docs

- [x] 2.1 Add the flag to the configuration reference and the chart reference.
- [x] 2.2 Add CHANGELOG entries for #1479/#1528, #1403, #1402 and #1478.

## 3. Verify

- [x] 3.1 `helm unittest charts/ncps`, `helm lint`, `task fmt` pass.
