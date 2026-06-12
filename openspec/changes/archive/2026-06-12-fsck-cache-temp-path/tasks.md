## 1. Code fix (TDD)

- [x] 1.1 Write a failing test asserting `fsckCommand()` registers a `cache-temp-path` string flag whose sources include config `cache.temp-path` and env `CACHE_TEMP_PATH` (assert flag presence + name; mirror how serve/migrate are wired).
- [x] 1.2 Add the `cache-temp-path` `cli.StringFlag` to `fsckCommand`'s flag list with `Sources: flagSources("cache.temp-path", "CACHE_TEMP_PATH")` and a usage string matching serve. Confirm `createCache` already consumes `cmd.String("cache-temp-path")` (no further code change expected).
- [x] 1.3 Run the new test green; confirm no existing fsck tests regress.

## 2. Docs

- [x] 2.1 Add a `--cache-temp-path` row (with `CACHE_TEMP_PATH` env, default = system temp) to the fsck flags reference in `docs/docs/User Guide/Operations/Integrity Check (fsck).md`, and note that hardened (read-only-root) deployments must point it at a writable directory.

## 3. Charts (verify, change only if required)

- [x] 3.1 Render/inspect the fsck CronJob (`charts/ncps/templates/fsck-cronjob.yaml`) and ConfigMap; confirm the writable temp volume is mounted at `.Values.config.cache.tempPath` and the ConfigMap emits `temp-path`. Conclude explicitly whether a chart change is needed; make the change only if the verification shows a gap.

## 4. Verification

- [x] 4.1 Run `task fmt`, `task lint`, and `task test`; confirm each exits zero.
- [x] 4.2 If `helm unittest charts/ncps` is part of the chart test surface and the chart was touched, run it; otherwise note it was not required.
