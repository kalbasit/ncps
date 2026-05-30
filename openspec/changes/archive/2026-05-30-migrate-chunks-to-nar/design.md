## Context

`migrate-nar-to-chunks` (`pkg/ncps/migrate_nar_to_chunks.go` + `Cache.MigrateNarToChunks`, cache.go:7527) converts whole NARs to CDC chunks and, after verification, deletes the whole file (and with `cdc.delete-delay` the whole file is GC'd). There is no reverse, so a deployment that enabled CDC cannot return to whole-file storage. This change adds `migrate-chunks-to-nar`, the mirror operation.

The building blocks already exist:
- Reconstruction: `getNarFromChunks` → `streamCompleteChunks` (cache.go:7011/7099) concatenates chunks in `chunk_index` order into a whole-NAR byte stream.
- Chunk store: `chunk.Store` exposes `HasChunk` / `GetChunk` / `PutChunk` / `DeleteChunk` (`pkg/storage/chunk/store.go`).
- Orphan detection: the existing GC keys deletion on "no remaining `nar_file_chunks` links" (`entchunk` with no `HasNarFileLinks`).
- Verification: `nar_file`/narinfo carry `NarHash` and `NarSize`.

## Goals / Non-Goals

**Goals:**
- A `Cache.MigrateChunksToNar(ctx, *nar.URL) error` that reconstructs → verifies → stores whole → flips the record → reclaims orphaned chunks, idempotently and resumably.
- A `migrate-chunks-to-nar` CLI command mirroring the forward command's flags and walk/concurrency structure.
- Verified-or-nothing: never store an unverified whole file, never delete chunks before the whole file is durable and the record flipped.

**Non-Goals:**
- No serving-path / automatic de-chunking; explicit operator command only.
- No removal of CDC or the forward command.
- No new DB schema (reuse `nar_file`, `nar_file_chunks`, `chunk`).

## Decisions

### 1. Reuse the existing reconstruction reader, wrapped in a verifying hasher
Drive reconstruction through the same path `streamCompleteChunks` uses (chunks in `chunk_index` order). Wrap the reader so the NAR hash and byte count are computed **as it streams** (`io.TeeReader` into a sha256 hasher, or hash-on-write). Compare to the recorded `NarHash`/`NarSize` only after the full stream is consumed. This guarantees the bytes written to the store are exactly the bytes hashed.

### 2. Write-then-flip-then-reclaim ordering (crash-safe)
Order operations so any interruption is recoverable by a re-run:
1. Reconstruct + verify into the NAR store via an **atomic** `PutNar` (temp + rename / S3 single-object put). No record change yet.
2. In one Ent transaction: set the `nar_file` to whole-file (`total_chunks = 0`, file_size/compression for the whole file) and delete its `nar_file_chunks` links.
3. Reclaim chunks that now have zero `nar_file_chunks` links.

A crash between (1) and (2): re-run sees the whole file present but the record still chunked → it re-verifies (cheap, store read) and completes (2)+(3). A crash between (2) and (3): re-run sees whole-file record + possibly orphaned chunks → it runs the reclaim sweep. Reclaim is idempotent (deleting an already-gone chunk is a no-op).

### 3. Idempotency via state detection, not a flag
`MigrateChunksToNar` first classifies the `nar_file`: already-whole (`total_chunks = 0`) → skip; chunked → migrate; whole-file-present-but-record-chunked → finish the flip. No migration-state column needed.

### 4. Dedup-safe reclamation reuses the existing orphan check
Delete a chunk only when no `nar_file_chunks` row references it (same predicate the current GC uses). Honor `cdc.delete-delay` semantics so a chunk is not yanked out from under an in-flight reader on another replica.

### 5. Command mirrors `migrate-nar-to-chunks`
Same flag set (`--dry-run`, storage local/s3, `--cache-database-url`, lock backend for coordination, `--concurrency`), same `narInfoStore.WalkNarInfos` + `errgroup` with `SetLimit(concurrency)` structure. Per-NAR errors are collected (not fatal to the batch); exit non-zero if any failed. Registered alongside the forward command in the root command.

### 6. Coordination with running instances
Like the forward command, optionally take the distributed (Redis) download lock per hash before migrating it, so a concurrently-serving replica does not race the record flip / chunk deletion. Without a lock backend, the command assumes it runs against a quiesced deployment (documented).

## Risks / Trade-offs

- **Reading every chunk per NAR** is inherent to reconstruction; on the slow backends this tool exists to escape, the migration is I/O-heavy but offline and concurrency-bounded.
- **Reclaiming shared chunks**: the orphan predicate must run against the post-flip link state; doing it inside/after the same transaction avoids a window where a shared chunk looks orphaned. Mitigated by decision #2 ordering + the existing GC predicate.
- **Atomic whole-file write on S3**: a single-object PUT is atomic; for local, temp-file + rename. Avoids a torn whole file ever being visible.
- **delete-delay vs immediate reclaim**: immediate deletion races replicas still reading chunks; deferring to the delete-delay/GC window is safer but slower to reclaim space. Default to the safe (deferred) behavior, with a flag to force immediate reclaim for quiesced deployments.
