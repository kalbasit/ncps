## 1. Red test (TDD)

- [x] 1.1 Add a package-internal test in `pkg/cache` that fabricates one completed `nar_file` with `total_chunks` above the SQLite parameter limit (> 32766) by inserting that many `chunk` + `nar_file_chunk` rows, then calls `streamCompleteChunks` and asserts it currently fails with `too many SQL variables` (RED against `main`).
- [x] 1.2 Add a test asserting the batched retrieval returns chunk hashes in ascending `chunk_index` order and preserves the completeness check (`storage.ErrNotFound` when a link is missing), including chunk counts that are both an exact multiple and a non-multiple of the batch size (page-boundary / off-by-one guard).

## 2. Implementation

- [x] 2.1 Add a `completeChunkQueryBatchSize` constant in `pkg/cache/cache.go` near `progressivePollBatchSize`, sized well under every driver limit (256–512).
- [x] 2.2 Replace the unbounded `NarFileChunk.Query()…WithChunk().All(ctx)` in `streamCompleteChunks` with a keyset walk: loop querying `NarFileID(id)` AND `ChunkIndexGT(last)`, `Order(ByChunkIndex())`, `Limit(completeChunkQueryBatchSize)`, `WithChunk()`; append each `link.Edges.Chunk.Hash` to `chunkHashes`; advance `last` to the last row's `ChunkIndex`; stop when a page returns fewer than the batch size.
- [x] 2.3 Keep the missing-edge guard (`errMissingChunkEdge`) and the `len(chunkHashes) != totalChunks → ErrNotFound` completeness check against the accumulated slice; leave `streamChunksWithPrefetch` unchanged.

## 3. Verification

- [x] 3.1 Confirm the red tests from §1 now pass (GREEN); confirm NARs below the limit still produce identical ordered hashes/bytes.
- [x] 3.2 Run `task fmt`, `task lint`, and `task test` (race detector) and confirm each exits zero.
- [x] 3.3 Run `openspec validate --change fix-serve-chunks-sql-var-limit --no-interactive` and confirm it passes.
