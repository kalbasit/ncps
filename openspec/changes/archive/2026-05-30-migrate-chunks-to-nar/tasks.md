## 1. Cache reconstruction + verify (TDD, vertical slices)

- [x] 1.1 `Cache.MigrateChunksToNar` reconstructs (chunks in `chunk_index` order via `getNarFromChunks`), verifies the streamed SHA-256 against the linked narinfo `NarHash` + `nar_file.file_size`, and writes the whole file via `narStore.PutNar` (bypassing `c.PutNar` to avoid re-chunking). RED→GREEN: `TestMigrateChunksToNar_ReconstructsVerifiesAndStoresWholeFile`.
- [x] 1.2 Hash mismatch aborts before any mutation (returns `ErrNarHashMismatch`; PutNar happens only after verification). Covered end-to-end by `TestMigrateChunksToNar_CLI_HashMismatchFailsWithoutDestroyingData` (chunks retained, non-zero exit).
- [~] 1.3 Missing chunk: covered by construction — any reconstruction error returns before `PutNar`/flip, so no mutation. Same abort-without-mutation property the §1.2 test demonstrates. No dedicated unit test added.

## 2. Record flip + crash-safe ordering

- [x] 2.1 After migrate the `nar_file` is whole-file (`total_chunks = 0`, no `nar_file_chunks` links). RED→GREEN: `TestMigrateChunksToNar_FlipsRecordToWholeFile`.
- [x] 2.2 Already-whole NAR returns `ErrNarAlreadyWholeFile` (early, `total_chunks == 0`); the CLI counts it as skipped.
- [~] 2.3 Resumability provided by construction: write (PutNar) → flip → reclaim ordering means an interrupted run re-runs the (idempotent) flip+reclaim; a whole file is only written after verification, so no short/corrupt file is ever exposed. No dedicated interrupted-run test added.

## 3. Dedup-safe chunk reclamation

- [x] 3.1 A chunk shared with another `nar_file` is retained. RED→GREEN: `TestMigrateChunksToNar_RetainsSharedChunks`.
- [x] 3.2 Orphaned chunks reclaimed via the existing `cleanupStaleLockChunks` (orphan predicate: `entchunk.Not(HasNarFileLinks())`). RED→GREEN: `TestMigrateChunksToNar_ReclaimsOrphanedChunks`.
- [x] 3.3 Reclamation is **deferred by default** — the migration flips the record and leaves now-orphaned chunks for the GC, so an in-flight chunk-serve is never truncated mid-stream — with an explicit **`--force-reclaim`** opt-in (for drained/maintenance-window runs) that reclaims immediately. Both paths are dedup-safe (chunks still referenced by another `nar_file` are retained).

## 4. CLI command `migrate-chunks-to-nar`

- [x] 4.1 `pkg/ncps/migrate_chunks_to_nar.go` mirrors `migrate_nar_to_chunks.go` (flags, `errgroup` with `SetLimit(concurrency)`); registered in the root command. Candidates are queried directly as chunked `nar_file`s (`total_chunks > 0`) rather than via `WalkNarInfos`.
- [x] 4.2 `--dry-run` makes no changes. RED→GREEN: `TestMigrateChunksToNar_CLI_DryRunMakesNoChanges`.
- [x] 4.3 Per-NAR failures are isolated (errgroup continues), counted, and the command exits non-zero. RED→GREEN: `TestMigrateChunksToNar_CLI_HashMismatchFailsWithoutDestroyingData`; success path: `TestMigrateChunksToNar_CLI_Success`.
- [x] 4.4 Coordination: `MigrateChunksToNar` takes a per-hash `downloadLocker.TryLock("migration-to-nar:"+hash)`; the command wires Redis via `getLockers` when configured.

## 5. Docs

- [x] 5.1 Documented in the command `Description` and in `README.md` (the storage-backend section), cross-referencing `migrate-nar-to-chunks` as the CDC round-trip.

## 6. Verify

- [x] 6.1 Cache + CLI tests pass under `-race`; `task fmt` / `task lint` (0 issues) / `task test` green.
- [x] 6.2 `openspec validate migrate-chunks-to-nar` passes.
