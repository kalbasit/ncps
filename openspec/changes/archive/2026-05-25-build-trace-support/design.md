## Context

ncps serves as a binary cache proxy for Nix. `nix copy` (and any client using CA derivations) unconditionally PUTs build trace entries to `/upload/build-trace-v2/{drvName}/{outputName}.doi` and expects to GET them back at `/build-trace-v2/{drvName}/{outputName}.doi`. ncps currently has no routes for these paths, returning 404, which causes `nix copy` to exit non-zero even when the NAR upload succeeded.

Build traces are the CA-derivations memoization table: a mapping from `(drvPath, outputName)` → `(outPath, signatures)`. They are small JSON blobs (~hundreds of bytes), never streamed like NARs, and are only client-pushed — ncps never fetches them from an upstream cache.

The existing narinfos + narinfo_signatures pattern in the Ent schema and cache layer provides the direct template for this feature.

## Goals / Non-Goals

**Goals:**
- Implement PUT/GET/HEAD for `build-trace-v2` so `nix copy` completes cleanly.
- Store entries in the database with structured columns and a `raw_json` safety valve.
- Append ncps's own signature on ingestion (matching narinfo behavior).
- All three DB dialects (SQLite, PostgreSQL, MySQL) supported via Ent + Atlas migrations.

**Non-Goals:**
- Verifying incoming signatures — tracked separately in issue #1269.
- Proxying build traces from upstream caches (build traces are push-only).
- Storage-backend (filesystem/S3) persistence — database only, no NAR-style file storage.
- Re-signing on GET (serve stored JSON with ncps's sig already embedded from PUT).

## Decisions

### 1. Database-only storage (no filesystem/S3)

Build trace entries are small, structured JSON blobs. Unlike NARs, there is no benefit to storing them as files — they have no streaming requirements and benefit from indexed lookups by `(drv_path, output_name)`. Storing them in the database gives atomic upsert, consistent reads, and the existing migration/tooling path.

**Alternatives considered:** Store as flat files under `build-trace-v2/` in the storage backend. Rejected — adds a storage interface method for a tiny payload, no query capability, and complicates the existing NarStore abstraction.

### 2. Ent schema: two new entities mirroring narinfos/narinfo_signatures

```text
build_trace_entries
  id           BIGINT / UUID  PK
  drv_path     TEXT           NOT NULL   -- e.g. "/nix/store/qwwz...-skopeo.drv" (full store path from body)
  output_name  TEXT           NOT NULL   -- e.g. "out"
  out_path     TEXT           NOT NULL   -- e.g. "/nix/store/xyz...-skopeo"
  raw_json     TEXT           NOT NULL   -- original upload body verbatim
  created_at   TIMESTAMP
  updated_at   TIMESTAMP
  UNIQUE (drv_path, output_name)

build_trace_signatures
  id                     PK
  build_trace_entry_id   FK → build_trace_entries (CASCADE DELETE)
  key_name               TEXT  NOT NULL   -- "cache.example.com-1"
  signature              TEXT  NOT NULL   -- base64-encoded Ed25519 sig bytes
```

The `UNIQUE (drv_path, output_name)` constraint means a second PUT for the same key is an upsert — update `out_path`, `raw_json`, and replace signatures (same as narinfos replacing their signatures on re-PUT).

**Alternatives considered:** Single table with signatures stored as a JSON column. Rejected — inconsistent with the existing narinfo_signatures pattern; harder to add per-signature indexes later.

### 3. Fingerprint computation for signing

Nix computes the build trace fingerprint as: the full JSON entry (key + value) with `signatures` removed from `value`, then JSON-serialized (compact, no indentation).

```go
type buildTraceKey   struct { DrvPath string `json:"drvPath"`; OutputName string `json:"outputName"` }
type buildTraceValue struct { OutPath string `json:"outPath"`; Signatures []btSig `json:"signatures,omitempty"` }
type buildTraceEntry struct { Key buildTraceKey `json:"key"`; Value buildTraceValue `json:"value"` }

func buildTraceFingerprint(e buildTraceEntry) (string, error) {
    e.Value.Signatures = nil
    b, err := json.Marshal(e)
    return string(b), err
}
```

ncps then calls `c.secretKey.Sign(nil, fingerprint)` — the same call used in `signNarInfo`. The resulting signature is serialized in the v2 format `{"keyName": "...", "sig": "..."}` matching what nix expects for build traces (vs the `"name:base64"` string format used for narinfos).

### 4. URL routing — two-segment path inside /upload

The path `build-trace-v2/{drvName}/{outputName}.doi` has two variable segments nested under the `/upload` prefix. Chi's route:

```go
r.Put("/build-trace-v2/{drvName}/{outputName}", s.putBuildTrace)
```

The `{outputName}` param will include the `.doi` suffix (chi does not strip extensions); the handler strips it with `strings.TrimSuffix(chi.URLParam(r, "outputName"), ".doi")`. The `{drvName}` segment contains dots (e.g., `skopeo-1.21.0.drv`) which chi handles correctly in a non-wildcard param.

Same pattern is registered at the root for GET/HEAD:

```go
router.Head("/build-trace-v2/{drvName}/{outputName}", s.getBuildTrace(false))
router.Get( "/build-trace-v2/{drvName}/{outputName}", s.getBuildTrace(true))
```

### 5. Cache layer interface

Three new methods on `*Cache`, following the narinfo naming convention:

```go
HasBuildTrace(ctx context.Context, drvName, outputName string) bool
GetBuildTrace(ctx context.Context, drvName, outputName string) ([]byte, error)
PutBuildTrace(ctx context.Context, drvName, outputName string, r io.Reader) error
```

`GetBuildTrace` reconstructs the JSON response from structured DB columns + signatures (not from `raw_json`) so that ncps's own signature is always present in the response. `raw_json` is stored for migration purposes only.

## Risks / Trade-offs

- **Experimental format** — build-trace-v2 JSON schema is explicitly marked experimental upstream. Mitigation: `raw_json` column allows re-parsing and re-migrating if the schema changes without losing data.
- **No upstream signature verification** — ncps appends its own signature unconditionally; a client trusting ncps's key will accept entries even if upstream sigs are invalid. Mitigation: tracked in issue #1269 as follow-up hardening.
- **Upsert on duplicate key** — a second PUT for the same `(drv_path, output_name)` replaces the entry. This is consistent with narinfo behavior but means ncps does not detect conflicting claims from different build agents. Mitigation: out of scope for now; coherence enforcement is a CA-derivations concern at the nix level.

## Migration Plan

1. Add two Ent schemas (`ent/schema/build_trace_entry.go`, `ent/schema/build_trace_signature.go`).
2. Run `go generate ./ent/...`.
3. Run `go run ./cmd/generate-migrations --name=add_build_trace_entries` to emit per-dialect SQL.
4. Deploy: `ncps migrate up` applies migrations automatically at startup. No data backfill needed (new tables).
5. Rollback: expand-contract policy applies — the new tables are additive. Dropping them requires a forward migration; no rollback command is provided.

## Open Questions

- Should a second PUT for the same `(drv_path, output_name)` with a **different** `out_path` be rejected (conflict) or silently overwrite? Current design: overwrite (consistent with narinfos). If conflict detection is desired, return `409 Conflict` instead.
- Should `GET` return `404` or `410 Gone` if the entry exists in `raw_json` but fails to re-parse under a future schema? Current design: `404` (treat as not found); log a warning.
