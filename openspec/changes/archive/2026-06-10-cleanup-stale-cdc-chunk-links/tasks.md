## 1. Reproduce (TDD red)

- [x] 1.1 Add a `pkg/ncps` test seeding: a dechunked nar_file (`total_chunks=0`) with a stale `nar_file_chunks` link to a chunk (blob in chunk store); a healthy chunked nar_file (`total_chunks>0`) with its link+chunk; and a chunk shared by both. Assert the reclaim removes only the stale link + its now-orphaned chunk, keeps the chunked NAR and the shared chunk, and is idempotent. Confirm it fails before the reclaim exists (RED).

## 2. Implement the reclaim (TDD green)

- [x] 2.1 Add `reclaimStaleChunkLinks(ctx, dbClient, chunkStore) (linksDeleted, chunksReclaimed int, err error)` in `pkg/ncps/fsck.go`: delete `nar_file_chunks` whose parent `nar_file` has `total_chunks <= 0` and `chunking_started_at` NULL; then delete chunks with `Not(HasNarFileLinks())` from the chunk store and DB.
- [x] 2.2 Invoke it from `repairFsckIssues` (under `--repair` + CDC mode) and log the counts.
- [x] 2.3 Confirm the test passes (GREEN), including idempotency.

## 3. Verify (no regressions)

- [x] 3.1 Existing fsck tests pass; chunked NARs untouched.
- [x] 3.2 `task fmt`, `task lint`, `task test` all green.
