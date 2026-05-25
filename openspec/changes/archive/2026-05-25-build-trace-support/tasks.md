## 1. Ent Schema

- [x] 1.1 Create `ent/schema/build_trace_entry.go` with fields: `drv_path`, `output_name`, `out_path`, `raw_json`, `created_at`, `updated_at`; unique index on `(drv_path, output_name)`; edge to `BuildTraceSignature`
- [x] 1.2 Create `ent/schema/build_trace_signature.go` with fields: `key_name`, `signature`; edge back to `BuildTraceEntry` with `ON DELETE CASCADE`
- [x] 1.3 Run `go generate ./ent/...` and verify the generated client compiles cleanly

## 2. Database Migrations

- [x] 2.1 Run `go run ./cmd/generate-migrations --name=add_build_trace_entries` to emit per-dialect Atlas migrations (SQLite, PostgreSQL, MySQL)
- [x] 2.2 Review generated SQL for all three dialects; confirm unique index and CASCADE constraint are present
- [x] 2.3 Run `ncps migrate up --dry-run` against SQLite, PostgreSQL, and MySQL to verify migrations apply without error

## 3. Build Trace Types and Fingerprint

- [x] 3.1 Define `BuildTraceEntry`, `BuildTraceKey`, `BuildTraceValue`, `BuildTraceSig` Go structs in `pkg/cache/` (or a sub-package) with JSON tags matching the v3 schema
- [x] 3.2 Implement `buildTraceFingerprint(entry BuildTraceEntry) (string, error)` — marshal entry with `value.signatures` nil, return JSON string
- [x] 3.3 Write unit tests for `buildTraceFingerprint` covering: standard entry, multiple existing sigs stripped, empty signatures field

## 4. Cache Layer

- [x] 4.1 Implement `HasBuildTrace(ctx, drvName, outputName string) bool` on `*Cache` — DB existence check
- [x] 4.2 Implement `GetBuildTrace(ctx, drvName, outputName string) ([]byte, error)` — load from DB, reconstruct JSON with all signatures
- [x] 4.3 Implement `PutBuildTrace(ctx, drvName, outputName string, r io.Reader) error` on `*Cache`:
  - Parse JSON body into `BuildTraceEntry`
  - Validate URL params match body (`key.drvPath` and `key.outputName`)
  - Compute fingerprint and append ncps's signature via `c.secretKey.Sign`
  - Upsert into `build_trace_entries` + replace `build_trace_signatures`
  - Store verbatim body in `raw_json`
- [x] 4.4 Write unit tests for `PutBuildTrace`: success, duplicate upsert, malformed JSON, URL/body mismatch
- [x] 4.5 Write unit tests for `GetBuildTrace`: found, not found, ncps signature present in response
- [x] 4.6 Write integration tests for all three DB dialects (SQLite, PostgreSQL, MySQL) covering PUT→GET roundtrip

## 5. Server Routes

- [x] 5.1 Add route constants `routeBuildTrace` (`/build-trace-v2/{drvName}/{outputName}`) to `pkg/server/server.go`
- [x] 5.2 Register `HEAD` and `GET` routes for `/build-trace-v2/{drvName}/{outputName}` in `registerRoutes`
- [x] 5.3 Register `PUT /build-trace-v2/{drvName}/{outputName}` inside the `/upload` sub-router (gated by `putPermitted`)
- [x] 5.4 Implement `getBuildTrace(withBody bool) http.HandlerFunc` — extract drvName + strip `.doi` from outputName, call `cache.GetBuildTrace`, return JSON
- [x] 5.5 Implement `putBuildTrace http.HandlerFunc` — check `putPermitted`, extract URL params, call `cache.PutBuildTrace`, return 200/400/403/500
- [x] 5.6 Write server handler tests: GET found, GET not found, HEAD found, PUT success, PUT 403, PUT 400 (bad body), PUT 400 (URL mismatch)

## 6. Lint, Format, and CI

- [x] 6.1 Run `golangci-lint run --fix` and resolve any remaining issues
- [x] 6.2 Run `nix fmt` to format all modified files
- [x] 6.3 Run `go test -race ./...` (with integration test env vars set) and confirm all tests pass
- [x] 6.4 Run `task ent:check` to verify Ent codegen is up to date and lints cleanly
- [x] 6.5 Run `go run ./cmd/atlas-sum-check --root .` to confirm all `atlas.sum` files are current
