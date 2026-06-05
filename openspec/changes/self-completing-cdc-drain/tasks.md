## 1. De-chunk: resolve NarHash by NAR hash (A)

- [x] 1.1 Extend `linkedNarinfoNarHash` (or a dedicated resolver) to fall back to a by-NAR-hash narinfo lookup (any URL / nar_hash present) when the join link and exact-none-URL lookup miss
- [x] 1.2 Test: a chunked NAR whose only NarHash-bearing narinfo has a `.nar.xz` URL de-chunks successfully

## 2. De-chunk: normalize narinfo URL on de-chunk (B)

- [x] 2.1 After the record flip in `MigrateChunksToNar`, update every referencing narinfo to `nar/<H>.nar` / Compression none / FileHash null
- [x] 2.2 Test: a de-chunked NAR's narinfo serves as none and a GET of `nar/<H>.nar` returns the whole NAR (no 404) even though `HasNarInChunks` is now false

## 3. De-chunk: purge-on-unverifiable (C)

- [x] 3.1 When no NarHash is resolvable from any narinfo, purge the chunked nar_file instead of returning the skip sentinel
- [x] 3.2 Broaden the driver loop (`migrate_chunks_to_nar.go`) so a hard reconstruction failure purges instead of counting `failed`/aborting
- [x] 3.3 Tests: no-NarHash NAR is purged (not skipped); corrupt-chunk NAR is purged (not failed); a full pass over a mixed fixture leaves `chunked = 0`

## 4. fsck residue repair (D)

- [ ] 4.1 Add an fsck check + `--repair` action for chunked nar_files: relink+normalize when a valid NarHash exists, purge otherwise
- [ ] 4.2 Tests: fsck normalizes a recoverable inconsistent chunked NAR; fsck purges an un-de-chunkable one

## 5. Verification

- [ ] 5.1 `task fmt`, `task lint`, `task test` pass
- [ ] 5.2 Deploy and run against the real production 116 stragglers; confirm `chunked → 0`, drain auto-completes on restart, and the previously-stuck paths serve
- [ ] 5.3 CDC-lifecycle e2e test exercises enable → drain → complete including residue classes
