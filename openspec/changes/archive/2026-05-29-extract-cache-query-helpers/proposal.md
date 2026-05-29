## Why

`pkg/cache/cache.go` is ~7,500 lines and inlines the same Ent queries repeatedly. The single-NarInfo-by-hash lookup (`NarInfo.Query().Where(entnarinfo.HashEQ(hash)).Only(ctx)`) appears verbatim at several sites against both `c.dbClient.Ent()` and `*ent.Tx`; chunk-by-hash batch reads (`Chunk.Query().Where(entchunk.HashIn(...)).All(ctx)`) recur; and the total-NAR-size aggregate (`Aggregate(ent.Sum(entnarfile.FieldFileSize))` + `sql.NullInt64` unpack) is duplicated as a multi-line block at two sites. The duplication makes the file harder to read and lets the copies drift.

## What Changes

- Add small unexported query helpers in `pkg/cache` that accept the Ent entity client (`*ent.NarInfoClient`, `*ent.NarFileClient`, `*ent.ChunkClient`) — which both `*ent.Client` and `*ent.Tx` expose — so the same helper serves transactional and non-transactional callers:
  - `narInfoByHash(ctx, q, hash) (*ent.NarInfo, error)` — the bare `.Where(HashEQ).Only` lookup.
  - `chunksByHashes(ctx, q, hashes) ([]*ent.Chunk, error)` — the `HashIn(...).All` batch read.
  - `totalNarFileSize(ctx, q) (int64, error)` — the aggregate query + `sql.NullInt64` unpack, returning 0 when empty. Each caller keeps its own error-handling policy (warn-and-continue vs. return).
- Replace the verbatim inline sites with these helpers.
- Leave eager-loaded / multi-predicate query variants (those chaining `.With...()` or additional `Where` clauses) untouched — they are not identical and would require option plumbing that adds more complexity than it removes.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `database-orm`: add a requirement that the common single-entity-by-hash lookups and the total-NAR-size aggregate are obtained through shared `pkg/cache` query helpers with a defined behavior contract, rather than re-inlined per call site.

## Impact

- Code: `pkg/cache/cache.go` (call sites), and a new `pkg/cache/queries.go` holding the helpers.
- No public API change, no schema/migration change, no runtime behavior change — identical `Only`/`All`/`Scan` semantics and identical error values.
- Tests: the existing `pkg/cache` suite (`cache_test.go`, `cache_internal_test.go`, `cdc_test.go`) already exercises these paths and is the safety net.

## Non-goals

- Not changing query semantics, eager-loading, ordering, or error types.
- Not consolidating eager-loaded or multi-predicate query variants.
- Not moving helpers onto `database.Client` (kept local to `pkg/cache` unless a second package needs them).

## I/O, latency, memory impact

None. The helpers issue the exact same SQL as the inline code; no extra round-trips, allocations, or buffering. This is a readability/DRY refactor only.
