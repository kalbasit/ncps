## Context

`streamCompleteChunks` (`pkg/cache/cache.go`) is the fast path for serving a NAR
that has finished CDC chunking (`total_chunks > 0`). It loads the NAR's chunk
links in a single query:

```go
links, err := c.dbClient.Ent().NarFileChunk.Query().
    Where(entnarfilechunk.NarFileID(int(narFileID))).
    Order(entnarfilechunk.ByChunkIndex()).
    WithChunk().          // eager-load → SELECT … FROM chunks WHERE id IN ($1 … $N)
    All(ctx)
```

`.WithChunk()` compiles to a follow-up `WHERE id IN (…)` with one bound parameter
per chunk. Once `N` exceeds the driver's parameter limit (SQLite 32766, verified
empirically for `mattn/go-sqlite3 v1.14.49`; PostgreSQL 65535) the query fails with
`too many SQL variables`. At the default CDC average of 64 KiB, a NAR over ~2 GiB
crosses the SQLite limit; issue #1463's ~8 GB NAR (~124k chunks) is far over. The
handler has already returned `HTTP 200` with a streaming body, so the client
receives a truncated NAR and reports `bad archive: unexpected end of nar`.

The fix pattern already lives in the same file. `streamProgressiveChunks` (the
in-progress path) walks chunk links in bounded pages with
`ChunkIndexGTE(idx)` + `Order(ByChunkIndex())` + `Limit(progressivePollBatchSize)`
(256), so its eager-load never binds more than 256 parameters. `fsck`'s
size-mismatch detection was fixed the same way (spec `fsck`: "MUST scale beyond
database driver parameter limits").

## Goals / Non-Goals

**Goals:**
- Serve completed chunked NARs of any chunk count on SQLite, PostgreSQL, and MySQL.
- Preserve exact chunk ordering, the completeness check, and byte-for-byte output.
- Reuse the batching idiom already established in the same file.

**Non-Goals:**
- No change to CDC sizing, the progressive path, `fsck`, or migrate commands.
- No new config flag; the batch size is a fixed internal constant.
- No lazy streaming of chunk hashes into the prefetch pipeline — the hash slice is
  still built eagerly (bounded by chunk count, as today); only the DB query is bounded.

## Decisions

**Decision: Replace the single unbounded query with a keyset walk over `chunk_index`.**
Loop: query `NarFileChunk` where `NarFileID == id` AND `ChunkIndexGT(last)`,
ordered by `chunk_index`, `Limit(batchSize)`, `WithChunk()`; append each
`link.Edges.Chunk.Hash` to `chunkHashes`; advance `last` to the last row's
`ChunkIndex`; stop when a page returns fewer than `batchSize` rows. Then hand
`chunkHashes` to the unchanged `streamChunksWithPrefetch`.

- *Why keyset over `OFFSET`*: `chunk_index` is monotonic per NAR and already the
  sort key; a `> last` cursor is an indexed range scan with stable ordering and no
  large-offset penalty. This matches `streamProgressiveChunks`.
- *Why keep the completeness check*: the existing
  `len(chunkHashes) != totalChunks → ErrNotFound` guard (spec
  `chunked-nar-serving-integrity`) still runs against the accumulated slice, so the
  integrity contract is unchanged.

**Decision: Introduce a dedicated batch-size constant (e.g. `completeChunkQueryBatchSize`).**
A fixed constant sized well under every driver limit (in the same 256–512 band as
the existing `progressivePollBatchSize`/`cdcCleanupHashBatchSize` constants). Not a
flag — no operational tuning need, and a hardcoded safe value keeps parity with the
other batch constants.
- *Alternative rejected*: reuse `progressivePollBatchSize` directly. Kept separate
  so the fast path can be tuned independently and the intent reads clearly at the
  call site.

**Decision: Cursor on `chunk_index`, not primary-key `id`.**
`chunk_index` is the ordering key and is dense/monotonic within a NAR; `WithChunk()`
still eager-loads by chunk `id` per page, bounded to `batchSize` parameters.

## Risks / Trade-offs

- **[More round-trips]** One `SELECT` becomes `ceil(chunks / batchSize)` queries
  (~243 at batchSize 512 for 124k chunks). → Each is a cheap covering-index range
  scan; cost is negligible against multi-GB stream transfer and overlaps the
  existing prefetch pipeline. Same trade-off already accepted for the progressive path.
- **[Ordering regression]** A wrong cursor/order could drop or duplicate chunks. →
  Mirrors the proven `streamProgressiveChunks` idiom; the completeness check
  (`count == total_chunks`) and a multi-batch order-preservation test guard it.
- **[Off-by-one at page boundary]** `GT(last)` must advance to the last row's index,
  not the loop counter. → Covered by a test whose chunk count is an exact multiple
  and a non-multiple of `batchSize`.

## Migration Plan

Pure query-shape change in one function. No schema, migration, ORM regeneration, or
API change. Ships in a normal release; rollback is reverting the commit. No data or
state migration. Deployable while the previous version still serves traffic.

## Open Questions

- Exact batch-size value (256 vs 512). Leaning 512 — halves round-trips versus the
  progressive path while staying ~64× under the SQLite limit. Final value chosen in
  implementation; either satisfies the spec.
