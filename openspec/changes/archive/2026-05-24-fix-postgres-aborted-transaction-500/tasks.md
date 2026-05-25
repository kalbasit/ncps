## 1. Write Failing Tests (TDD)

- [x] 1.1 Add `testRecordChunkBatch_PreExistingChunk`: seed a chunk row in DB,
  call `recordChunkBatch` with a batch containing that hash, assert no error
  and the `nar_file_chunks` link uses the pre-existing chunk's ID
- [x] 1.2 Add `testRecordChunkBatch_DuplicateHashInBatch`: build a batch where
  two entries share the same hash, assert `recordChunkBatch` returns no error
  and both `nar_file_chunks` links reference the same chunk ID
- [x] 1.3 Add `testRecordChunkBatch_NewChunk`: assert that a brand-new chunk
  hash inserts a new `chunks` row and the `nar_file_chunks` link is created
- [x] 1.4 Verify all new tests fail (RED) before touching production code
  (Note: tests start GREEN because the sequence-desync bug is environmental
  and not reproducible in unit tests; tests serve as regression coverage)

## 2. Fix Chunk Insert in recordChunkBatch

- [x] 2.1 In `pkg/cache/cache.go` `recordChunkBatch`, replace the per-chunk
  UpsertOne (`OnConflictColumns(entchunk.FieldHash).Update(...)`) with
  `Create().OnConflictColumns(entchunk.FieldHash).Ignore().Exec(ctx)`
- [x] 2.2 After each INSERT, query the chunk ID via
  `tx.Chunk.Query().Where(entchunk.Hash(chunkMetadata.Hash)).Only(ctx)`
  and use `.ID` for the `nar_file_chunks` link
- [x] 2.3 Run new tests — verify they pass (GREEN)

## 3. Fix 25P02 Connection Leak

- [x] 3.1 Add `database.IsAbortedTransactionError(err)` helper in
  `pkg/database/errors.go` that detects SQLSTATE 25P02
- [x] 3.2 In `WithTransaction` (`pkg/database/client.go`), after fn(tx)
  fails and rollback is called, if the error is 25P02 issue a bare
  `ROLLBACK` via `c.sdb.ExecContext(context.Background(), "ROLLBACK")`
  to clean up any aborted connection in the pool (best-effort)
- [x] 3.3 Skipped: the DO NOTHING fix eliminates the root cause; a meaningful
  25P02 pool test is non-deterministic without connection-level control over
  database/sql. IsAbortedTransactionError is exported for future callers.

## 4. Lint and Test

- [x] 4.1 `golangci-lint run --fix ./pkg/cache/... ./pkg/database/...`
- [x] 4.2 `nix fmt`
- [x] 4.3 `go test -race -run "TestRecordChunkBatch\|TestCacheBackends" ./pkg/cache/...`
- [x] 4.4 `go test -race ./pkg/database/...`

## 5. Commit and Update Spec

- [ ] 5.1 Commit with `/git-commit`
- [ ] 5.2 Update `openspec/specs/cdc-chunking/spec.md` with the delta spec
  content (sync via `/opsx:archive`)
