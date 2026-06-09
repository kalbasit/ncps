# Capability Spec: CDC Chunking

## Purpose

Defines requirements for Content-Defined Chunking (CDC) of NAR files, including ingestion
validation, error handling, and data integrity guarantees.
## Requirements
### Requirement: CDC ingestion MUST validate total byte count before committing
CDC ingestion MUST validate the total uncompressed byte count against the declared NarSize before committing.

When CDC chunking completes (the chunker signals end-of-stream by closing `chunksChan`),
the system SHALL compare the total accumulated uncompressed bytes (`totalSize`) against
the narinfo's declared `NarSize` (`fileSize` parameter). If `fileSize > 0` and
`uint64(totalSize) != fileSize`, the system MUST:
- Return an error wrapping `io.ErrUnexpectedEOF` with a message that includes both the
  expected and actual byte counts.
- NOT call `UpdateNarFileTotalChunks` (leave `total_chunks = 0`, `chunking_started_at`
  set, so the lock-liveness CDC recovery reclaims the orphan once the per-hash migration
  lock is free — within one recovery interval rather than after a fixed 1-hour age gate).
- Log the mismatch at `error` level including the narinfo hash and both byte counts.

If `fileSize == 0`, the validation MUST be skipped (narinfo with unknown declared size).

#### Scenario: Early-EOF truncation is rejected at commit
- **WHEN** the decompressed NAR stream ends before `fileSize` bytes are consumed (e.g., upstream HTTP/2 stream drops with GOAWAY)
- **THEN** `storeNarWithCDCFromReader` returns a non-nil error
- **AND** the `nar_file` row retains `total_chunks = 0` (not committed as complete)
- **AND** an error-level log entry records the expected vs actual byte count

#### Scenario: Complete NAR is committed normally
- **WHEN** the decompressed NAR stream produces exactly `fileSize` uncompressed bytes before EOF
- **THEN** `storeNarWithCDCFromReader` calls `UpdateNarFileTotalChunks` with the correct count
- **AND** `nar_file.total_chunks > 0` after the call
- **AND** no error is returned

#### Scenario: NarSize is zero — validation is skipped
- **WHEN** `fileSize` parameter passed to `storeNarWithCDCFromReader` is 0
- **THEN** the size validation step is skipped entirely
- **AND** `UpdateNarFileTotalChunks` is called regardless of how many bytes were chunked

### Requirement: GetNarInfo MUST normalize compression in-memory during lazy-chunking transition

When lazy CDC chunking is enabled, `GetNarInfo` MUST normalize the narinfo in-memory. Narinfos are initially stored in the DB with the
upstream compression (e.g., `Compression: xz`) while the NAR is chunked in the
background. After chunking completes, the DB update is asynchronous. During this window,
`GetNarInfo` MUST normalize the narinfo in-memory before returning so that clients
receive consistent `Compression: none` / `.nar` URL without waiting for the DB update.

The normalization SHALL:
- Call `HasNarInChunks` (a lightweight indexed query checking `total_chunks > 0`)
- If the NAR is already chunked: rewrite `Compression`, `URL`, `FileHash`, and `FileSize`
  in memory only — the DB record is NOT modified synchronously
- Normalize the URL hash before the `HasNarInChunks` lookup to handle nix-serve-style
  prefixed hashes (e.g., `abc-hash` → `hash`) matching nar_file rows correctly
- Skip normalization entirely when CDC is disabled

#### Scenario: GetNarInfo normalizes when NAR is already chunked

- **GIVEN** CDC is enabled
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz`
- **AND** the NAR has been migrated to CDC chunks (`HasNarInChunks` returns true)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo has `Compression: none` and URL `nar/hash.nar`
- **AND** `FileHash` is nil and `FileSize` is 0
- **AND** the narinfo DB record is NOT modified synchronously

#### Scenario: GetNarInfo does NOT normalize when NAR is not yet chunked

- **GIVEN** CDC is enabled
- **AND** a narinfo exists in the DB with `Compression: xz` and URL `nar/hash.nar.xz`
- **AND** the NAR is NOT in CDC chunks (`HasNarInChunks` returns false)
- **WHEN** `GetNarInfo` is called for that hash
- **THEN** the returned narinfo retains `Compression: xz` and the original URL
- **AND** background migration is triggered

#### Scenario: No normalization when CDC is disabled

- **GIVEN** CDC is disabled
- **AND** a narinfo exists in the DB with `Compression: xz`
- **WHEN** `GetNarInfo` is called
- **THEN** the returned narinfo retains the original `Compression: xz`

### Requirement: GetNar MUST return 404 for compressed URL when NAR exists only as chunks

The system SHALL return 404 for a compressed URL when the NAR exists only as chunks. When CDC is enabled and a client requests a NAR by its compressed URL (e.g.,
`nar/hash.nar.xz`), but the NAR exists only as CDC chunks (no whole-file in storage),
the system SHALL return `storage.ErrNotFound` rather than attempting to serve
uncompressed chunk data as compressed content. Serving chunk data with mismatched
compression would cause Nix to fail with "input compression not recognized".

#### Scenario: xz NAR request returns 404 when only chunks exist

- **GIVEN** CDC is enabled
- **AND** `nar_file.total_chunks > 0` (NAR is chunked)
- **AND** no whole-file `.nar.xz` exists in storage
- **WHEN** `GetNar` is called with a `.nar.xz` URL
- **THEN** the response is `storage.ErrNotFound`
- **AND** chunk data is NOT served

#### Scenario: xz NAR request succeeds when whole-file is still in storage

- **GIVEN** CDC is enabled with lazy chunking
- **AND** `nar_file.total_chunks > 0` (NAR is chunked)
- **AND** the whole-file `.nar.xz` still exists in storage (lazy chunking preserves it)
- **WHEN** `GetNar` is called with a `.nar.xz` URL
- **THEN** the whole-file xz content is served from storage

### Requirement: CDC chunk insert MUST use ignore-on-conflict, not upsert

CDC chunk inserts SHALL use ignore-on-conflict, not upsert. When recording a batch of chunks in `recordChunkBatch`, each chunk insert
into the `chunks` table SHALL use `ON CONFLICT (hash) DO NOTHING` (not
`DO UPDATE`). Chunks are content-addressed immutable blobs; an existing
chunk with the same hash is already correct and MUST NOT be touched.

After the INSERT (whether it inserted or skipped), the system SHALL retrieve
the chunk's `id` via a `SELECT WHERE hash = <hash>` query and use that ID
to build the `nar_file_chunks` link. This two-step approach ensures the
correct ID is obtained regardless of whether the row was newly inserted or
pre-existed.

#### Scenario: Chunk is new — INSERT succeeds, ID retrieved from SELECT

- **WHEN** a chunk with a given hash does not exist in the `chunks` table
- **THEN** the INSERT inserts the row and `DO NOTHING` is not triggered
- **AND** the subsequent SELECT retrieves the newly inserted chunk's `id`
- **AND** the `nar_file_chunks` link is created with that `id`

#### Scenario: Chunk already exists — INSERT is skipped, ID retrieved from SELECT

- **WHEN** a chunk with a given hash already exists in the `chunks` table
- **THEN** the INSERT is skipped silently (`DO NOTHING`)
- **AND** the subsequent SELECT retrieves the pre-existing chunk's `id`
- **AND** the `nar_file_chunks` link is created with that `id`

#### Scenario: Duplicate hash in same batch — no conflict error

- **WHEN** a chunk batch contains two entries with the same hash (same content)
- **THEN** the first INSERT inserts the row; the second INSERT is skipped
- **AND** both `nar_file_chunks` links use the same chunk `id`
- **AND** no error is returned

### Requirement: Transaction failure MUST NOT leave connections in aborted state

A transaction failure MUST NOT leave connections in an aborted state. After any transaction in `withEntTransactionRetry` fails (whether immediately
or after all retry attempts are exhausted), the system SHALL ensure the
database connection is returned to the pool in a clean, non-aborted state.

If the final transaction error carries PostgreSQL SQLSTATE 25P02
(`in_failed_sql_transaction`), the system SHALL issue an explicit `ROLLBACK`
on the connection before it is returned to the pool. A subsequent query on
the same connection MUST NOT fail with "current transaction is aborted".

#### Scenario: Transaction exhausts retries — connection is clean on return

- **GIVEN** `withEntTransactionRetry` exhausts all retry attempts
- **AND** the final error is a PostgreSQL unique_violation (SQLSTATE 23505)
- **WHEN** the error is returned to the caller
- **THEN** the connection is returned to the pool in a clean state
- **AND** a subsequent non-transactional query on any pooled connection
  succeeds without a 25P02 error

#### Scenario: 25P02 detected — explicit rollback issued

- **GIVEN** a database connection is in PostgreSQL aborted-transaction state
  (SQLSTATE 25P02 — "in_failed_sql_transaction")
- **WHEN** `withEntTransactionRetry` detects this condition on the error
- **THEN** an explicit `ROLLBACK` is issued on that connection
- **AND** the connection is returned to the pool usable for the next query

### Requirement: CDC goroutine error MUST be logged at error level
The background CDC goroutine MUST log non-nil chunking errors at error level, except `ErrMigrationInProgress` which is a benign no-op.

When the background CDC goroutine (started from `pullNarIntoStore`) receives a non-nil
error from `storeNarWithCDCFromReader` (or the simple-path `storeNarWithCDC`), it SHALL
log the error at `error` level (not `debug` or `warn`) and SHALL propagate it via
`downloadState.setError`. The log entry MUST include the narinfo hash and NAR URL.

This SHALL NOT apply to `ErrMigrationInProgress`: that error means a peer (another
replica, or a concurrent `MigrateNarToChunks` / `MigrateChunksToNar` / stale-recovery for
the same hash) holds the per-hash migration lock, which is a benign "someone else owns
it" outcome in a multi-instance fleet — the in-flight client already received the bytes
and the lock holder will persist the NAR. When the background error
`errors.Is(err, ErrMigrationInProgress)`, the goroutine SHALL log at `debug`/`info` level
and SHALL NOT call `downloadState.setError`. This applies to both the pipe path and the
simple-path copy.

#### Scenario: Truncated CDC fails with visible log
- **WHEN** `storeNarWithCDCFromReader` returns a non-nil error that is not
  `ErrMigrationInProgress` inside the CDC goroutine
- **THEN** a log entry at level `error` is emitted with the NAR hash and error message
- **AND** `downloadState.setError` is called with that error
- **AND** no success log ("download of nar complete") is emitted for that NAR

#### Scenario: Peer-held migration lock is a benign no-op
- **WHEN** background CDC chunking returns an error for which
  `errors.Is(err, ErrMigrationInProgress)` is true (a peer holds the per-hash lock)
- **THEN** no `error`-level log entry is emitted for it
- **AND** `downloadState.setError` is NOT called
- **AND** the outcome is recorded at `debug`/`info` level at most

### Requirement: Placeholder nar_file records MUST NOT be treated as servable

Placeholder `nar_file` records MUST NOT be treated as servable. A `nar_file` row with `total_chunks = 0` and `chunking_started_at` NULL is a **placeholder**
created by `storeInDatabase` at narinfo-fetch time before any NAR bytes have been downloaded or
chunked. Such a placeholder SHALL NOT be considered servable by any read path. Specifically:

- The `hasAsset` callback used by `coordinateDownload`/`prePullNar` SHALL return `false` for a
  placeholder, so coordination does not return an already-completed (`closed`) download state.
- `serveNarFromStorageViaPipe` SHALL NOT be entered for a placeholder when no whole-file exists
  in the store and no chunks exist.

Only a whole-file in storage, `total_chunks > 0`, or chunking actively in progress within
`cdcChunkingLockTTL` makes the NAR servable.

#### Scenario: Placeholder is not reported as an available asset

- **GIVEN** CDC is enabled
- **AND** a `nar_file` placeholder row exists for hash `H` (`total_chunks = 0`, `chunking_started_at` NULL)
- **AND** no whole-file and no chunks exist for `H`
- **WHEN** `coordinateDownload`'s `hasAsset` is evaluated for `H`
- **THEN** it SHALL return `false`
- **AND** `coordinateDownload` SHALL proceed to download `H` rather than returning a completed state

#### Scenario: Placeholder does not cause a 2ms not-found

- **GIVEN** a `nar_file` placeholder row for hash `H` with no backing data
- **WHEN** `GetNar` is called for `H`
- **THEN** the system SHALL NOT call `serveNarFromStorageViaPipe` and return `storage.ErrNotFound`
  without first attempting an upstream download

### Requirement: Stuck chunking records MUST be recoverable

Orphaned chunking records whose migration lock is free MUST be recoverable, regardless of age.

A `nar_file` row whose chunking was started but never completed (`total_chunks = 0` with
`chunking_started_at` set, and no whole-file present) is an **orphan**. Liveness is
determined by the per-hash migration lock (`migrationLockKey(hash)`), not by the row's
age: an orphan whose lock is free belongs to a dead chunker and is recoverable; an orphan
whose lock is held belongs to a live chunker and MUST be left alone.

Orphan rows whose lock is free SHALL be recoverable: the CDC lazy-recovery job and/or the
next `GetNar` SHALL reset the row (clearing `chunking_started_at` and removing partial
`nar_file_chunks`) so a clean download/chunking can occur, or re-drive it to completion. A
recoverable orphan SHALL NOT permanently cause reads to fail, and SHALL NOT wait for a
fixed age gate (`chunking_started_at` older than `cdcChunkingLockTTL`) before becoming
eligible for recovery.

#### Scenario: Orphan with a free lock is re-driven by recovery

- **GIVEN** a `nar_file` row for hash `H` with `total_chunks = 0` and `chunking_started_at`
  set (any age)
- **AND** no whole-file for `H` exists in the store
- **AND** no instance holds `migrationLockKey(H)`
- **WHEN** the CDC lazy-recovery job processes `H`
- **THEN** it SHALL reset the row for a clean retry (clear `chunking_started_at`, remove
  partial chunks) or re-drive download/chunking for `H`
- **AND** after recovery a `GetNar` for `H` SHALL succeed if upstream has the NAR

#### Scenario: Orphan served on demand

- **GIVEN** an orphan `nar_file` row for hash `H` with a free lock
- **WHEN** a client requests `GET /nar/H...`
- **THEN** `GetNar` SHALL re-attempt the upstream download rather than returning a terminal 404

### Requirement: CDC startup validation MUST allow enabled→disabled transition

CDC startup validation MUST allow the enabled→disabled transition. When CDC configuration is validated at startup via `ValidateOrStoreCDCConfig`, the
system SHALL permit the transition from a stored `cdc_enabled=true` to a current
`enabled=false`. The system SHALL return nil without modifying any stored configuration
keys. The four stored CDC config keys (`cdc_enabled`, `cdc_min`, `cdc_avg`, `cdc_max`)
SHALL remain intact in the database so that drain mode can initialize the chunk store
on every subsequent restart and `migrate-chunks-to-nar` can proceed concurrently.

The updated validation rules are:
- If no stored CDC config exists and `enabled=false`: no-op, return nil.
- If no stored CDC config exists and `enabled=true`: store the new config (first boot), return nil.
- If stored config exists and `enabled=true`: validate that all four stored values match current values; return error on mismatch.
- If stored config exists and `enabled=false` (enabled→disabled transition): return nil, leave all stored keys intact.

#### Scenario: Disabling CDC after being enabled returns nil and preserves stored config

- **GIVEN** `cdc_enabled=true` is stored in the configuration database
- **WHEN** `ValidateOrStoreCDCConfig` is called with `enabled=false`
- **THEN** it SHALL return nil (no error)
- **AND** the configuration database SHALL still contain `cdc_enabled=true`
- **AND** `cdc_min`, `cdc_avg`, `cdc_max` SHALL remain unchanged

#### Scenario: Keeping CDC enabled with matching config succeeds

- **GIVEN** `cdc_enabled=true` is stored with matching min/avg/max values
- **WHEN** `ValidateOrStoreCDCConfig` is called with `enabled=true` and the same sizes
- **THEN** it SHALL return nil

#### Scenario: Keeping CDC enabled with mismatched sizes fails

- **GIVEN** `cdc_enabled=true` is stored with `cdc_min=16384`
- **WHEN** `ValidateOrStoreCDCConfig` is called with `enabled=true` and `minSize=32768`
- **THEN** it SHALL return a non-nil error describing the mismatch

### Requirement: Unrecoverable backing-less placeholder rows MUST be garbage-collected

The recovery process SHALL garbage-collect a backing-less placeholder `nar_file` row —
`total_chunks = 0`, `chunking_started_at` NULL, no whole-file in the store — once it is
**provably unrecoverable**, removing the row together with its `narinfo_nar_files` link,
so such rows do not accumulate in the database or get re-scanned by the CDC lazy-recovery
sweep indefinitely.

"Provably unrecoverable" means the NAR is confirmed genuinely absent upstream (a
definitive not-found, not a timeout or transient failure). A row SHALL NOT be removed
while the NAR can still be served by an upstream: `GetNar` MUST remain able to re-create
the placeholder and download the NAR on demand after collection. A transient or timeout
upstream failure SHALL NOT be treated as genuine absence.

Collection SHALL be bounded (rate-limited by the recovery interval and batch size) and
SHALL only consider rows older than the recovery cutoff, so it cannot race a freshly
created placeholder for an in-flight download.

#### Scenario: Genuinely-absent placeholder is collected

- **GIVEN** a backing-less placeholder `nar_file` row for hash `H` older than the recovery cutoff
- **AND** no whole-file for `H` exists in the store
- **AND** the upstream returns a definitive not-found for `H`
- **WHEN** the recovery process evaluates `H`
- **THEN** the placeholder row and its `narinfo_nar_files` link SHALL be removed
- **AND** the removal SHALL leave no dangling foreign-key reference

#### Scenario: Placeholder whose NAR upstream still has is NOT collected

- **GIVEN** a backing-less placeholder `nar_file` row for hash `H`
- **AND** an upstream still has the NAR for `H`
- **WHEN** the recovery process evaluates `H`
- **THEN** the placeholder row SHALL NOT be removed
- **AND** a later `GetNar` for `H` SHALL re-download and serve it

#### Scenario: Transient upstream failure does not trigger collection

- **GIVEN** a backing-less placeholder `nar_file` row for hash `H`
- **AND** the upstream existence check fails transiently (timeout / connection reset)
- **WHEN** the recovery process evaluates `H`
- **THEN** the placeholder row SHALL NOT be removed
- **AND** `H` SHALL remain eligible for a future re-evaluation

### Requirement: Serving a whole-file NAR MUST be resilient to a concurrent background migration

The system SHALL serve a NAR that it observed as present even if a concurrent background
NAR→chunks migration removes the whole file mid-serve.

When `GetNar` observes a NAR present as a whole file in the store while CDC is enabled, it
MAY trigger a background NAR→chunks migration that, on completion, deletes the whole file
from the store. The synchronous serve that follows MUST NOT surface
`storage.ErrNotFound` to the caller solely because that background migration removed the
whole file between the time-of-check (`HasNarInStore`) and the time-of-use
(`getNarFromStore`).

The system SHALL treat a whole-file store read that misses, whenever a chunk store is
available (CDC enabled OR drain mode) and the request is for the uncompressed NAR
(`Compression == none`), as a signal that the NAR has been migrated, and SHALL fall back to
serving the NAR by reassembling it from chunks. Gating on chunk-store availability rather
than on CDC writes being enabled ensures the fallback also protects reads during drain mode,
where chunked NARs remain servable. Only when neither the whole file nor a reassemblable set
of chunks exists MAY `GetNar` return `storage.ErrNotFound`.

A chunk-path failure that is NOT a not-found result (e.g. a database or storage error) MUST
be surfaced to the caller rather than masked as the original store not-found, matching how
the direct chunk-serve path propagates its errors. Both `storage.ErrNotFound` and an ent
not-found result count as "the NAR is absent" and preserve the original store not-found.

This fallback applies exclusively to the uncompressed serve path. A request for a
compressed NAR (e.g. `.nar.xz`) whose whole file is gone MUST continue to resolve to
`storage.ErrNotFound`, because chunks are stored uncompressed and cannot reconstruct a
compressed stream (consistent with the existing chunked-serving rules).

#### Scenario: Whole-file deleted by background migration mid-serve falls back to chunks

- **GIVEN** a NAR for hash `H` stored as a whole file while CDC was disabled
- **AND** CDC is subsequently enabled
- **WHEN** `GetNar(H)` is called and the triggered background migration deletes the whole
  file from the store before the synchronous `getNarFromStore` opens it
- **THEN** the serve path detects the store miss and reassembles `H` from its chunks
- **AND** `GetNar` returns the complete NAR bytes with no error
- **AND** `storage.ErrNotFound` is NOT surfaced to the caller

#### Scenario: Whole-file store miss in drain mode falls back to chunks

- **GIVEN** a NAR for hash `H` whose chunks are committed and reassemblable
- **AND** the cache is in drain mode (CDC writes disabled, chunk store still configured)
- **WHEN** the whole-file store read for `H` misses
- **THEN** the serve path falls back to reassembling `H` from its chunks
- **AND** `storage.ErrNotFound` is NOT surfaced to the caller

#### Scenario: Mixed-mode retrieval after enabling CDC always succeeds

- **GIVEN** a blob NAR stored with CDC disabled and a separate NAR stored with CDC enabled
- **WHEN** both NARs are retrieved via `GetNar` after CDC is enabled
- **THEN** each retrieval returns its exact original content
- **AND** neither retrieval fails with `error fetching the nar from the store: not found`,
  regardless of background-migration timing

#### Scenario: Genuinely absent NAR still returns not found

- **GIVEN** a hash `H` that has neither a whole file in the store nor any chunks
- **WHEN** `GetNar(H)` is called in upload-only mode (no upstream pull)
- **THEN** `GetNar` returns `storage.ErrNotFound`

#### Scenario: Compressed request after migration still returns not found

- **GIVEN** a NAR whose whole file has been migrated to (uncompressed) chunks
- **WHEN** a request for the compressed NAR (`Compression != none`) is served
- **THEN** the serve path returns `storage.ErrNotFound` rather than serving raw chunk bytes

### Requirement: Serving a whole-file NAR MUST be resilient to a stale time-of-check store-presence observation

The system SHALL serve a NAR whose whole file is present at serve time even if an
earlier time-of-check observed it absent. `GetNar` computes a store-presence flag
(`hasNarInStore`) once and then re-evaluates servability via `isServable`, which
performs its own fresh `HasNarInStore` check. When the whole file lands in the
store between those two checks, the first flag is stale (`false`) while the NAR is
in fact present and servable. The serve path MUST NOT treat that stale `false` as
authoritative and route an uncompressed request to the chunk store.

The system SHALL guarantee that an uncompressed serve request is routed to the
chunk store ONLY when a chunk store is available. When no chunk store is
configured, the serve path MUST serve from the whole-file store (re-evaluating
store presence as needed) rather than calling the chunk-serve path. It MUST NOT
surface `chunk store not initialized, cannot serve NAR from chunks` for a NAR
whose whole file is present.

`GetNar` MAY only return `storage.ErrNotFound` for such a request when the whole
file is genuinely absent from the store AND (no chunk store is configured OR the
chunks cannot be reassembled). A NAR that is observed servable but whose backing
cannot actually produce bytes MUST fall through to the normal cache-miss recovery
(re-download), not surface a chunk-store-unavailable error.

This requirement is the inverse complement of "Serving a whole-file NAR MUST be
resilient to a concurrent background migration": that requirement covers
present-at-check / deleted-before-use (fall back store→chunks); this one covers
absent-at-check / present-before-use (do not route to chunks; serve the present
whole file). Both eliminate reliance on a single stale `hasInStore` observation.

#### Scenario: Whole-file lands between time-of-check and serve, no chunk store

- **GIVEN** CDC is disabled (no chunk store is configured)
- **AND** a NAR for hash `H` is being downloaded so its whole file is briefly absent from the store
- **WHEN** `GetNar(H)` observes `hasNarInStore = false`, then the whole file lands in the store, and `isServable` subsequently observes the whole file present
- **THEN** `GetNar` serves `H` from the whole-file store and returns the complete NAR bytes with no error
- **AND** the error `chunk store not initialized, cannot serve NAR from chunks` is NOT surfaced

#### Scenario: Uncompressed serve never routes to chunks when no chunk store exists

- **GIVEN** no chunk store is configured
- **WHEN** the serve path handles an uncompressed (`Compression == none`) request whose stale `hasInStore` flag is `false`
- **THEN** the serve path resolves against the whole-file store, not `getNarFromChunks`
- **AND** no `chunk store not initialized` error is produced

#### Scenario: Genuinely absent NAR with no chunk store still recovers via re-download

- **GIVEN** no chunk store is configured
- **AND** a hash `H` whose whole file is genuinely absent from the store but available upstream
- **WHEN** `GetNar(H)` is called (not in upload-only mode)
- **THEN** `GetNar` falls through to the upstream re-download path and serves `H` successfully
- **AND** it does NOT return `chunk store not initialized`

#### Scenario: Genuinely absent NAR in upload-only mode still returns not found

- **GIVEN** no chunk store is configured
- **AND** a hash `H` that has neither a whole file in the store nor any upstream source
- **WHEN** `GetNar(H)` is called in upload-only mode
- **THEN** `GetNar` returns `storage.ErrNotFound`
- **AND** it does NOT return `chunk store not initialized`

### Requirement: CDC narinfo URL normalization MUST NOT be predicted at store time

When a narinfo is pulled from upstream, the system SHALL persist the narinfo URL and compression that reflect the NAR's actual stored representation. The system SHALL NOT rewrite the persisted narinfo URL to `nar/<hash>.nar` (Compression none) at store time merely because CDC is enabled — because the asynchronous chunking that would make `none` true may not have completed, or may never complete. Store-time normalization to `none` SHALL be performed only when the upstream narinfo itself advertises `Compression: none` (the genuinely-uncompressed / Harmonia case, stored as zstd and served as none with transparent encoding).

Presentation of `url=none` for a chunked CDC NAR is provided exclusively at serve time by `maybeCDCNormalizeNarInfoURL`, gated on `HasNarInChunks` returning true. Consequently a CDC narinfo whose NAR is not yet chunked is served with its truthful upstream compression, and one whose NAR is chunked is served as `none`.

#### Scenario: Eager-CDC pull of an xz upstream narinfo persists the truthful xz URL

- **GIVEN** CDC is enabled (eager, lazy chunking disabled)
- **AND** an upstream narinfo with `Compression: xz` and URL `nar/<H>.nar.xz`
- **AND** the NAR has not (yet) been chunked
- **WHEN** the narinfo is pulled and persisted
- **THEN** the persisted narinfo URL SHALL be `nar/<H>.nar.xz`
- **AND** the persisted compression SHALL be `xz`
- **AND** the persisted narinfo SHALL NOT advertise a `none` URL

#### Scenario: Chunked CDC NAR is still served as none at serve time

- **GIVEN** a narinfo persisted with `Compression: xz`
- **AND** its NAR has been chunked (`HasNarInChunks` returns true)
- **WHEN** the narinfo is served via `GetNarInfo`
- **THEN** the served narinfo SHALL present `Compression: none` and URL `nar/<H>.nar`
- **AND** the persisted DB record SHALL remain unchanged (presentation is serve-time only)

#### Scenario: Genuinely-uncompressed upstream is still normalized to none

- **GIVEN** an upstream narinfo with `Compression: none`
- **WHEN** the narinfo is pulled and persisted
- **THEN** the persisted narinfo SHALL have `Compression: none` and URL `nar/<H>.nar`

### Requirement: Completing a CDC chunking operation MUST reconcile the narinfo↔nar_file link

The `narinfo_nar_files` link is created in the narinfo-write path, which can race the asynchronous chunking that finalizes the `nar_file` row and leave the chunked `nar_file` unlinked. After a chunking operation finalizes a `nar_file` (and on the post-store narinfo reconciliation it triggers), the system SHALL ensure that every narinfo whose URL references that NAR is linked to the finalized `nar_file`, creating the `narinfo_nar_files` link when missing. The reconciliation SHALL be idempotent — when the link already exists it is a no-op — so it does not alter steady-state behavior.

#### Scenario: Chunking completion creates the missing link

- **GIVEN** a finalized chunked `nar_file` for hash `H` (`total_chunks > 0`)
- **AND** a narinfo whose URL is `nar/<H>.nar`
- **AND** no `narinfo_nar_files` link between them
- **WHEN** `checkAndFixNarInfosForNar` runs for the NAR (as invoked on chunking completion)
- **THEN** a `narinfo_nar_files` link SHALL be created between that narinfo and the `nar_file`

#### Scenario: Reconciliation is a no-op when the link already exists

- **GIVEN** a `nar_file` for hash `H` already linked to its narinfo
- **WHEN** `checkAndFixNarInfosForNar` runs for the NAR
- **THEN** the existing link SHALL be preserved unchanged
- **AND** no duplicate link SHALL be created

### Requirement: A de-chunked NAR's narinfo MUST be consistent with its whole-file storage

Once a NAR has been converted from chunks to whole-file (`Compression:none`) storage, the persisted narinfo for that NAR SHALL advertise the Compression:none URL (`nar/<H>.nar`) with FileHash null and FileSize null, matching the actual storage. The narinfo SHALL NOT be left advertising a chunk-era or different-compression URL whose servability depended on `maybeCDCNormalizeNarInfoURL` (which only rewrites while the NAR is still chunked).

#### Scenario: Serving a de-chunked NAR does not depend on serve-time chunk normalization

- **GIVEN** a NAR that has been de-chunked to `none/whole` storage
- **AND** its narinfo advertised `nar/<H>.nar.xz` before de-chunking
- **WHEN** the narinfo is served after de-chunking (the NAR is no longer chunked, so `HasNarInChunks` is false)
- **THEN** the served narinfo SHALL advertise `nar/<H>.nar` (Compression none)
- **AND** a GET of that URL SHALL return the whole NAR, not 404

### Requirement: CDC orphan recovery MUST gate reaping on per-hash migration-lock liveness, not age

The CDC lazy-recovery job SHALL gate orphan reaping on per-hash migration-lock liveness, not on row age.

The CDC lazy-recovery job (`runCDCLazyRecovery`, run from a cron and serialized across
instances by `withTryLock("cdc-lazy-recovery")`) reclaims orphaned mid-chunking rows — a
`nar_file` row with `total_chunks = 0` and `chunking_started_at` set, left behind when a
chunker crashed (SIGKILL / OOM) before completing.

In a multi-replica shared-database deployment, such a row may instead belong to a **live**
chunker on a peer instance: `total_chunks` is `0` for the entire duration of a healthy
chunking operation, so the row state alone cannot distinguish a dead chunker from a live
one. Recovery therefore MUST NOT use the row's age as a proxy for liveness, and MUST NOT
require `chunking_started_at` to be older than `cdcChunkingLockTTL` before reaping.

Instead, the download-path chunker SHALL hold the per-hash migration lock
(`migrationLockKey(hash)`) for the duration of chunking, and recovery SHALL use that lock
as the sole cross-instance liveness signal:

- Recovery SHALL select in-progress orphan candidates (`total_chunks = 0` AND
  `chunking_started_at` IS NOT NULL) of **any age** — there SHALL be no
  `chunking_started_at < now - cdcChunkingLockTTL` predicate on the candidate query.
- Before mutating a candidate, recovery SHALL `TryLock(migrationLockKey(hash))`
  (non-blocking).
- If the lock is **already held** (a peer is actively chunking, or another recovery owns
  it), recovery SHALL skip the row, leave its `chunking_started_at` and partial
  `nar_file_chunks` untouched, and move on.
- If the lock is **acquired** (no live chunker), recovery SHALL, while holding the lock,
  reap the row: delete its partial `nar_file_chunks`, reclaim the now-orphaned chunk
  blobs, and clear `chunking_started_at`, reverting the row to `total_chunks = 0`,
  `chunking_started_at = NULL`. Recovery SHALL release the lock afterward.
- A reverted row SHALL be re-driven to completion by the on-demand `GetNar` re-download
  path or a subsequent recovery pass; recovery itself need not re-download.

A crashed chunker's lock SHALL become acquirable once it is released or its TTL
(`cdcChunkingLockTTL`) expires, so a genuinely dead chunker's orphan is reclaimed within
one cron interval rather than only after a fixed age gate.

#### Scenario: Fresh orphan from a dead chunker is reclaimed without waiting for an age gate

- **GIVEN** a `nar_file` row for hash `H` with `total_chunks = 0`, `chunking_started_at`
  set to a recent time (well under `cdcChunkingLockTTL`), and partial `nar_file_chunks`
- **AND** no instance currently holds `migrationLockKey(H)` (the chunker crashed)
- **WHEN** the CDC lazy-recovery job processes `H`
- **THEN** recovery SHALL acquire `migrationLockKey(H)` via `TryLock`
- **AND** delete the partial `nar_file_chunks` for `H` and reclaim their chunk blobs
- **AND** clear `chunking_started_at`, leaving `total_chunks = 0`
- **AND** a subsequent `GetNar` for `H` SHALL re-download from upstream successfully

#### Scenario: A peer's live in-flight chunking row is never reaped

- **GIVEN** a `nar_file` row for hash `H` with `total_chunks = 0` and `chunking_started_at`
  set, while a peer instance is actively chunking `H` and holds `migrationLockKey(H)`
- **WHEN** the CDC lazy-recovery job processes `H`
- **THEN** recovery's `TryLock(migrationLockKey(H))` SHALL fail
- **AND** recovery SHALL skip `H`, leaving `chunking_started_at` and the in-flight
  `nar_file_chunks` untouched
- **AND** the peer's chunking SHALL be free to complete normally

### Requirement: Recovery skip counters MUST distinguish lazy-disabled from lock-held skips

Recovery skip counters MUST distinguish lazy-chunking-disabled skips from lock-held (live-chunker) skips.

The lazy-recovery job emits per-run counters in its completion log. The count of
candidates skipped because a peer holds the per-hash migration lock (a live-chunker
stale-lock skip) MUST be tracked and reported separately from the count of
whole-file-backed candidates skipped because lazy chunking is disabled. The
`stale_recovery_skip_count` field SHALL count only the former; a distinct field (e.g.
`lazy_chunking_disabled_skip_count`) SHALL count the latter. The two SHALL NOT be folded
into a single counter.

#### Scenario: Lazy-disabled skip does not inflate the stale-lock skip counter

- **GIVEN** CDC lazy chunking is disabled
- **AND** a recovery candidate `H` is a placeholder (`chunking_started_at` NULL) with a
  whole-file present in the store
- **WHEN** the recovery job processes `H` and skips it because lazy chunking is disabled
- **THEN** the run's `stale_recovery_skip_count` SHALL NOT be incremented for `H`
- **AND** a distinct lazy-chunking-disabled skip counter SHALL be incremented instead

#### Scenario: Lock-held skip increments the stale-lock skip counter

- **GIVEN** a recovery candidate `H` with `chunking_started_at` set whose
  `migrationLockKey(H)` is held by a peer
- **WHEN** the recovery job processes `H` and skips it because the lock is held
- **THEN** the run's `stale_recovery_skip_count` SHALL be incremented for `H`

### Requirement: Read path MUST treat a fresh dead-chunker orphan as not servable

The read path MUST gate servability of an in-progress chunking row on migration-lock liveness, not on lock age.

A `nar_file` row with `total_chunks = 0` and a non-stale `chunking_started_at` is only
servable if a chunker is actually producing its chunks. The download-path chunker holds
the per-hash migration lock (`migrationLockKey(hash)`) for the entire chunking operation
(`withNarMigrationLock`), so a **free** lock means the producer died mid-chunking (issue
#1230) — the row is a dead orphan, not a live chunk. `isServable` MUST therefore, for the
ambiguous `total_chunks == 0 && chunking_started_at != NULL` case, probe the migration
lock and report the row **not servable** when the lock is free, so `GetNar` re-downloads
the NAR cleanly from upstream instead of entering chunk-serving (which commits a `200`
response plus chunks `0..M` and then stalls `maxWaitPerChunk` on a chunk that never
arrives, surfacing `Truncated zstd input` to the client).

When the migration lock is **held** (a live producer on this or a peer instance), the
in-progress row MUST remain servable so legitimate slow/streaming chunking still serves.
Fully chunked rows (`total_chunks > 0`) and whole-file-in-store rows MUST remain servable
without a lock probe (the probe is scoped to the ambiguous case to keep it off the hot
path). On a probe error, the row MUST be treated as live (fail safe: preserve the
existing wait behavior rather than discard a possibly-live chunk).

#### Scenario: Fresh orphan with a free lock is not servable (re-download instead of stall)

- **GIVEN** CDC is enabled and a `nar_file` row for hash `H` with `total_chunks = 0` and a
  recent `chunking_started_at`, no whole-file in the store
- **AND** no instance holds `migrationLockKey(H)` (the chunker crashed)
- **WHEN** `isServable` is evaluated for `H`
- **THEN** it SHALL return `false`
- **AND** `GetNar` SHALL fall through to an upstream re-download rather than serving partial chunks

#### Scenario: Fresh in-progress row with a live producer remains servable

- **GIVEN** a `nar_file` row for hash `H` with `total_chunks = 0` and a recent
  `chunking_started_at`, while a producer holds `migrationLockKey(H)`
- **WHEN** `isServable` is evaluated for `H`
- **THEN** it SHALL return `true` so chunk-serving streams the in-flight chunks

### Requirement: CDC chunking takeover MUST reclaim an orphan held under the migration lock regardless of age

CDC chunking takeover MUST reclaim a prior in-progress orphan based on migration-lock ownership, not lock age.

Every caller of `findOrCreateNarFileForCDC` (the download path via `withNarMigrationLock`,
`putNarWithCDC`, and `MigrateNarToChunks`) holds `migrationLockKey(hash)` for the duration
of the operation. Because a live chunker would still hold that lock, reaching
`findOrCreateNarFileForCDC` proves any existing `chunking_started_at` on a
`total_chunks = 0` row belongs to a chunker that is no longer running. The takeover logic
MUST therefore reclaim the orphan — delete its partial `nar_file_chunks`, reclaim the
orphaned chunk blobs, and restart chunking — **regardless of the lock's age**. It MUST NOT
refuse a fresh (`age < cdcChunkingLockTTL`) in-progress row with `ErrAlreadyExists`;
doing so would strand the orphan and prevent a re-download (triggered by the read-path
liveness gate above) from ever re-chunking it.

#### Scenario: Re-chunk takes over a fresh orphan under the migration lock

- **GIVEN** a `nar_file` row for hash `H` with `total_chunks = 0`, a recent
  `chunking_started_at`, and partial `nar_file_chunks`
- **AND** the caller holds `migrationLockKey(H)`
- **WHEN** `findOrCreateNarFileForCDC` processes `H`
- **THEN** it SHALL reclaim the partial `nar_file_chunks` and proceed to re-chunk
- **AND** it SHALL NOT return `ErrAlreadyExists`

### Requirement: GetNar MUST advertise the compression of the bytes it serves

When `GetNar` serves a NAR from the whole-file store (not from chunks), the compression it reports to the caller MUST describe the bytes actually streamed. The serve path MUST NOT relabel the served bytes using a CDC-first database lookup that may return a different representation's compression (e.g. a chunked `compression=none` row that coexists with the whole file during a lazy-chunking transition). Concretely, the response compression MUST equal the compression of the file actually opened, after accounting for transparent zstd→none decompression.

This is the NAR-serve counterpart to "GetNarInfo MUST normalize compression in-memory during lazy-chunking transition": together they keep the narinfo's advertised `Compression`/`URL`, the NAR response's compression, and the streamed bytes mutually consistent so `nix` never sees a size shortfall or an unrecognized compression.

#### Scenario: Whole-file xz NAR served while a chunked none row coexists

- **WHEN** CDC is enabled, a NAR exists as a whole xz file AND a chunked `compression=none` row for the same hash coexists (the lazy-chunking transition window), and a client requests the xz NAR
- **THEN** `GetNar` streams the xz file bytes and reports `Compression: xz` (not `none`), with a byte count equal to the xz file size, so the client decompresses to exactly `NarSize`

#### Scenario: Whole-file none NAR served transparently from zstd

- **WHEN** a client requests an uncompressed (`none`) NAR that is physically stored as `.nar.zst` and there is no chunked representation
- **THEN** `GetNar` transparently decompresses and reports `Compression: none`, matching the uncompressed bytes it streams

#### Scenario: Enable-CDC-on-existing-cache upgrade path serves correctly

- **WHEN** a cache that already holds whole-file (xz) NARs has CDC enabled and a client fetches a closure whose paths include those pre-existing whole-file NARs
- **THEN** every such NAR is served with a compression label matching its bytes, and `nix` completes the fetch without `NAR ... is incomplete` or `input compression not recognized`

### Requirement: Read path MUST prefer in-flight staging over progressive chunk streaming during the chunking window

During the eager-CDC chunking window (`total_chunks == 0`), when in-flight staging parts are available for the NAR hash, the read path SHALL serve from the staging parts in preference to `streamProgressiveChunks`. Progressive chunk streaming SHALL remain the fallback used only when no staging parts are present. This precedence SHALL NOT change steady-state serving once `total_chunks > 0`.

#### Scenario: Staging present during chunking — prefer staging

- **WHEN** a NAR hash has `total_chunks == 0` (actively chunking)
- **AND** in-flight staging parts for that hash are available in shared storage
- **THEN** the read path SHALL serve from the staging parts
- **AND** it SHALL NOT enter `streamProgressiveChunks` for that request

#### Scenario: No staging present during chunking — fall back to progressive chunks

- **WHEN** a NAR hash has `total_chunks == 0`
- **AND** no in-flight staging parts are available (feature disabled, uncontended, or already reclaimed)
- **THEN** the read path SHALL fall back to `streamProgressiveChunks` as before

#### Scenario: Steady state unchanged

- **WHEN** a NAR hash has `total_chunks > 0`
- **THEN** serving SHALL proceed from chunks as before, regardless of any staging state

### Requirement: The NAR download lock MUST be held through the eager-CDC chunking window

For an eager-CDC NAR (chunked progressively during download, with no whole-file copy committed to shared storage), the replica performing the download SHALL retain the per-hash NAR download lock until chunking completes, rather than releasing it when the raw bytes are first stored (the start of chunking). Holding the lock through the chunking window SHALL cause a cross-pod reader arriving mid-chunking to contend for the lock — entering download coordination and recording an in-flight staging request — instead of acquiring a free lock and short-circuiting to chunk-based serving.

The eager-CDC in-flight bytes are decompressed (the temp is decompressed for chunking), so a request for the **uncompressed** (`none`) variant SHALL be served the complete NAR from in-flight staging, while a request for a **compressed** variant (e.g. `.nar.xz`) — which cannot be reconstructed from decompressed bytes (ncps has no NAR compressor, and a re-compressed file would not match the narinfo `FileHash`/`FileSize`) — SHALL return not-found so the client falls back to an upstream that has the original compressed file.

This SHALL NOT change download-lock release timing for non-CDC or lazy-CDC downloads, which have no progressive chunking window. Holder death during the chunking window SHALL remain recoverable through download-lock TTL expiry and takeover, unchanged.

#### Scenario: Download lock is held until chunking completes (eager CDC)

- **WHEN** a replica downloads an eager-CDC NAR for hash `H` and begins chunking it (its `nar_file` row is created with `total_chunks == 0`)
- **THEN** the replica SHALL continue to hold the per-hash NAR download lock until chunking completes
- **AND** a cross-pod reader attempting to coordinate `H` during that window SHALL contend for the lock rather than acquire it

#### Scenario: Cross-pod uncompressed request mid-chunking serves the complete NAR from staging

- **WHEN** replica A holds the download lock and is actively chunking an eager-CDC NAR for hash `H`
- **AND** replica B receives a request for the uncompressed (`none`) variant of `H`
- **THEN** replica B SHALL contend for the lock, record an in-flight staging request, and serve the complete NAR from staging once parts are available, rather than fragile progressive chunk reassembly

#### Scenario: Cross-pod compressed request mid-chunking falls back to upstream, not corrupt or 404-from-chunks

- **WHEN** replica A is actively chunking an eager-CDC NAR for hash `H` and replica B receives a request for a compressed variant (e.g. `.nar.xz`) of `H`
- **THEN** replica B SHALL return a not-found from the staging serve path so the client falls back to an upstream that has the original compressed file
- **AND** replica B SHALL NOT serve the decompressed in-flight bytes mislabeled as the requested compression

#### Scenario: Non-CDC and lazy-CDC download-lock timing unchanged

- **WHEN** a NAR is downloaded with CDC disabled, or with lazy chunking enabled
- **THEN** the download-lock release timing SHALL be unchanged from prior behavior, because there is no progressive chunking window in which the whole NAR is absent from shared storage

#### Scenario: Holder death during chunking is recoverable

- **WHEN** the replica holding the download lock dies while chunking an eager-CDC NAR for hash `H`
- **THEN** the download lock SHALL expire via its TTL
- **AND** another replica SHALL be able to take over and re-download the NAR, unchanged from prior takeover behavior

#### Scenario: Steady-state compressed-only-as-chunks 404 is unchanged

- **WHEN** a NAR is finished chunking (`total_chunks > 0`), its whole-file copy is gone, and a client requests a compressed variant
- **THEN** the serve path SHALL still return HTTP 404 so the client falls back to an upstream cache, unchanged by the held-lock behavior

