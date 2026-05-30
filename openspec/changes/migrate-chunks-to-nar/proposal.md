## Why

ncps can migrate whole NAR files into CDC chunks (`migrate-nar-to-chunks`), but the path is one-way: with `cdc.delete-delay` the original whole files are GC'd after chunking, so once a deployment is chunked there is **no supported way back to whole-file storage**. Operators who enabled CDC and then find it counter-productive for their backend (high-latency / spinning-disk / network filesystem — see the storage-backend guidance added in `fix-nar-serving-failures`) are stranded: disabling CDC leaves chunk-only NARs that the whole-file path cannot serve, and the chunk small-file overhead cannot be reclaimed. This change provides the reverse migration so CDC can be safely exited.

## What Changes

- Add a `migrate-chunks-to-nar` CLI command (mirror of `migrate-nar-to-chunks`): for each chunked `nar_file`, reconstruct the whole NAR from its ordered chunks, **verify the reconstructed bytes against the recorded NAR hash/size**, write the whole file to storage, update the `nar_file`/narinfo records to the whole-file representation, and (optionally) reclaim now-unreferenced chunks.
- Reconstruction MUST be **idempotent and resumable**: re-running skips already-reconstructed NARs; an interrupted run leaves no half-written whole files and no orphaned/missing data.
- Chunk reclamation MUST respect cross-NAR dedup — a chunk is only deleted when no remaining `nar_file` references it (honoring the same `delete-delay` semantics as the forward path).
- Multi-dialect (sqlite/postgres/mysql) DB updates via the existing Ent/transaction helpers.
- Dry-run / batch-size / concurrency flags consistent with `migrate-nar-to-chunks`.

## Capabilities

### New Capabilities
- `chunks-to-nar-migration`: reconstruct whole NAR files from CDC chunks, verified against the recorded hash, with idempotent/resumable execution and dedup-safe chunk reclamation, so a deployment can exit CDC.

### Modified Capabilities
<!-- none — additive command; serving behavior is unchanged. -->

## Impact

- Code: new `pkg/ncps/migrate_chunks_to_nar.go` (command) + a `Cache.MigrateChunksToNar` (or equivalent) reconstruction method in `pkg/cache/`; reuses chunk enumeration (`NarFileChunk` ordered by `chunk_index`), the chunk store, and NAR hashing (`pkg/nar`). Registered in the root command alongside `migrate-nar-to-chunks`.
- Data: rewrites `nar_file` rows from chunked to whole-file representation and deletes dedup-safe chunks; no schema/migration change (uses existing tables).
- **I/O / latency / memory**: an offline batch operation (not on the serving path). Reconstruction streams chunk→whole-file (O(1) memory per NAR); reads every chunk once and writes each whole file once. Verification adds one hash pass per NAR. Runs at operator-chosen concurrency.

## Non-goals

- Not automatic/triggered de-chunking on the serving path — this is an explicit operator-run command.
- Not removing or changing CDC chunking, `migrate-nar-to-chunks`, or the serving paths.
- Not a storage-backend migration tool (local<->s3); it operates within the configured backend.
