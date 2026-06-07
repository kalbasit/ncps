## ADDED Requirements

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

## MODIFIED Requirements

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
