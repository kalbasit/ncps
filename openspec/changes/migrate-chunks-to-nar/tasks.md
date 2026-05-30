## 1. Cache reconstruction + verify (TDD, vertical slices)

- [ ] 1.1 RED→GREEN: `Cache.MigrateChunksToNar(ctx, *nar.URL)` reconstructs a chunked NAR (chunks in `chunk_index` order), verifies the streamed bytes against the recorded `NarHash`/`NarSize`, and `PutNar`s the whole file atomically. Test: a CDC-chunked NAR is migrated and `GetNar` then serves the whole file. (Reuse the `setupSQLiteFactory` + chunk-store harness from `cdc_test.go`/`streaming_abort_internal_test.go`.)
- [ ] 1.2 RED→GREEN: hash mismatch aborts — no whole file stored, no chunk deleted, record unchanged. (Inject a chunk whose bytes don't match.)
- [ ] 1.3 RED→GREEN: a missing chunk aborts cleanly (error, no mutation).

## 2. Record flip + crash-safe ordering

- [ ] 2.1 RED→GREEN: after a successful migrate the `nar_file` is whole-file (`total_chunks = 0`, no `nar_file_chunks` links) and serves via the whole-file path.
- [ ] 2.2 RED→GREEN (idempotency): an already-whole NAR is skipped with no changes.
- [ ] 2.3 RED→GREEN (resumability): with the whole file already in the store but the record still chunked (simulated interrupted run), re-running finishes the flip + reclaim without producing a corrupt/short file.

## 3. Dedup-safe chunk reclamation

- [ ] 3.1 RED→GREEN: a chunk shared with another still-chunked NAR is retained after migrating one referencer.
- [ ] 3.2 RED→GREEN: a now-orphaned chunk (no remaining `nar_file_chunks` links) is reclaimed. Reuse the existing orphan predicate (`entchunk` with no `HasNarFileLinks`).
- [ ] 3.3 Honor `cdc.delete-delay` (default: defer reclaim to the GC window; flag to force immediate reclaim for a quiesced deployment).

## 4. CLI command `migrate-chunks-to-nar`

- [ ] 4.1 Add `pkg/ncps/migrate_chunks_to_nar.go` mirroring `migrate_nar_to_chunks.go`: flags (`--dry-run`, storage local/s3, `--cache-database-url`, lock backend, `--concurrency`, `--cache-temp-path`), `narInfoStore.WalkNarInfos` + `errgroup` with `SetLimit(concurrency)`, calling `MigrateChunksToNar` per NAR. Register in the root command.
- [ ] 4.2 RED→GREEN: `--dry-run` reports planned migrations/reclamations and makes no changes (no PutNar, no record mutation, no chunk deletion).
- [ ] 4.3 RED→GREEN: a per-NAR failure is recorded, does not abort the batch, and the command exits non-zero reporting the failed hashes.
- [ ] 4.4 Optional per-hash distributed (Redis) lock acquisition for coordination with running instances, mirroring the forward command.

## 5. Docs

- [ ] 5.1 Document `migrate-chunks-to-nar` (purpose: exit CDC back to whole-file) in the CLI help/Description and README, cross-referencing the storage-backend guidance from `fix-nar-serving-failures` and the forward `migrate-nar-to-chunks`.

## 6. Verify

- [ ] 6.1 New tests pass under `-race`; `task fmt` / `task lint` / `task test` green.
- [ ] 6.2 `openspec validate migrate-chunks-to-nar` passes.
