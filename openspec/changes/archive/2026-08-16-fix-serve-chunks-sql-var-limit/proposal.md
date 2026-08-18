## Why

Serving a large chunked NAR fails with `error getting chunks: too many SQL variables` (issue #1463). The completed-chunk fast serve path loads every chunk link for a NAR in one unbounded query, so any NAR with more chunks than the database driver's bound-parameter limit (SQLite 32766, PostgreSQL 65535) can never be served — the client gets an `HTTP 200` with a truncated body and `bad archive: unexpected end of nar`. At the default CDC average of 64 KiB, this triggers for any NAR larger than ~2 GiB on SQLite.

## What Changes

- Replace the unbounded eager-load in `streamCompleteChunks` (`pkg/cache/cache.go`) — `NarFileChunk.Query()…WithChunk().All(ctx)` — with a bounded keyset walk over `chunk_index`, using `ChunkIndexGT(last)` + `Limit(batchSize)` and accumulating chunk hashes page by page. This mirrors the fix already shipped for `fsck` (`chunksForNarFile`) and the batching already present in the progressive serve path (`streamProgressiveChunks`, `Limit(256)`).
- The completed-chunk fast serve path becomes independent of chunk count: a NAR with any number of chunks reassembles and serves correctly on SQLite, PostgreSQL, and MySQL.
- No change to ordering, completeness checks, streaming/prefetch behavior, or wire output — the produced chunk-hash sequence is byte-for-byte identical to today's for NARs under the limit.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `chunked-nar-serving-integrity`: Add a requirement that the completed-chunk fast serve path MUST reassemble NARs whose chunk count exceeds the database driver's bound-parameter limit, by querying chunk links in bounded batches rather than a single unbounded parameterized query.

## Impact

- **Code**: `pkg/cache/cache.go` — `streamCompleteChunks` query only. No schema, migration, ORM-regeneration, or API-surface change.
- **Behavior**: Fixes #1463. NARs previously unservable (chunk count > driver limit) now serve; all smaller NARs unaffected.
- **I/O / latency**: The single `SELECT` becomes `ceil(chunks / batchSize)` sequential indexed range queries (e.g. ~243 queries at batchSize 512 for a 124k-chunk / 8 GB NAR). Each is a cheap covering-index scan; the added round-trips are negligible against multi-GB stream transfer and overlap with the existing prefetch pipeline.
- **Memory**: Unchanged-to-lower peak. Chunk hashes are still collected into one slice (bounded by chunk count, as today), but Ent no longer materializes all junction+chunk entity rows at once — it holds one batch at a time.

## Non-goals

- Not changing CDC chunk sizing, the chunking algorithm, or default parameters.
- Not streaming chunk hashes lazily into the prefetch pipeline (the hash slice is still built eagerly); only the DB query is bounded.
- Not touching the progressive path, `fsck`, or the migrate commands, which already batch.
- Not adding a configurable batch-size flag; a fixed internal constant (well under every driver limit) suffices.
