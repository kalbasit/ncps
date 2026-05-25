## Why

`nix copy` unconditionally PUTs build-trace data to `/build-trace-v2/…` endpoints that ncps doesn't implement; the resulting 404s make `nix copy` exit non-zero even when the actual NAR upload succeeded, breaking CI pipelines. Build traces are a CA-derivations memoization table mapping derivation inputs to output store paths — implementing them properly lets ncps serve as a full binary cache for CA-derivation workloads.

## What Changes

- Add `PUT /upload/build-trace-v2/{drvName}/{outputName}.doi` — stores a build trace entry, gated behind the existing `putPermitted` flag.
- Add `GET /build-trace-v2/{drvName}/{outputName}.doi` — serves stored entries.
- Add `HEAD /build-trace-v2/{drvName}/{outputName}.doi` — existence check.
- On PUT, ncps appends its own signature (same pattern as narinfos — incoming signatures are stored as-is without verification; signature verification is a separate hardening concern tracked in [issue #1152]).
- New `build_trace_entries` and `build_trace_signatures` database tables (structured columns + `raw_json` safety valve for format changes).
- No new config flags needed — reuses `putPermitted`.

## Capabilities

### New Capabilities

- `build-trace`: Full PUT/GET/HEAD support for the nix `build-trace-v2` binary cache protocol. Stores structured build trace entries (drv path, output name, out path, signatures) in the database. ncps appends its own signature on ingestion, matching narinfo behavior.

### Modified Capabilities

- `api-surface`: New routes under `/build-trace-v2/` and `/upload/build-trace-v2/` added to the routing table.
- `data-model`: Two new tables — `build_trace_entries` (drv_path, output_name, out_path, raw_json) and `build_trace_signatures` (key_name, signature) — added to the Ent schema, following the narinfos/narinfo_signatures pattern.

## Impact

- **`ent/schema/`** — two new Ent schema files; `go generate ./ent/...` required.
- **`migrations/`** — new per-dialect Atlas migrations (SQLite, PostgreSQL, MySQL).
- **`pkg/cache/`** — `HasBuildTrace`, `GetBuildTrace`, `PutBuildTrace` methods; `signBuildTrace` helper using the existing `secretKey`.
- **`pkg/server/`** — three new route handlers; URL parsing must handle `.drv/out.doi` path segments correctly.
- **No new dependencies** — fingerprint computation is pure JSON marshaling; signing reuses `secretKey.Sign()`.
- **Performance**: entries are small JSON blobs (~hundreds of bytes); no streaming or compression concerns.

> **Note:** This feature is gated behind nix's experimental `ca-derivations` flag, so only users of CA derivations will interact with these endpoints. The JSON format is marked experimental upstream and may change; `raw_json` storage allows re-parsing if the schema evolves.
