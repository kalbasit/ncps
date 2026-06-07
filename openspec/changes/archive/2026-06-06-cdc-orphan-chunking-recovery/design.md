## Context

`runCDCLazyRecovery` (cron, serialized by `withTryLock("cdc-lazy-recovery")`) reclaims
orphaned mid-chunking `nar_file` rows (`total_chunks = 0`, `chunking_started_at` set). PR
#1317's current implementation selects in-progress orphans only when
`chunking_started_at < now - cdcChunkingLockTTL` (a 1h age gate) and additionally
`TryLock`s `migrationLockKey(hash)` per row in `recoverStaleCDCChunkingLock`. The
download-path chunker now holds that same per-hash migration lock while chunking
(`storeNarWithCDCFromReaderWithMigrationLock`), so the lock — not age — is the real
cross-instance liveness signal. The 1h gate is redundant for safety and, per the original
#1230 intent, harmful: it delays healing fresh orphans for up to an hour while the read
path serves `Truncated zstd input`.

## Goals / Non-Goals

**Goals:**
- Reclaim a dead chunker's orphan within one recovery interval, regardless of age.
- Keep multi-replica shared-DB deployments safe via the migration-lock liveness check.
- Stop benign `ErrMigrationInProgress` (peer holds lock) from logging at error level and
  setting a download error.
- Disambiguate the lazy-disabled skip from the lock-held skip in recovery metrics.

**Non-Goals:**
- No heartbeat protocol; the existing migration lock is the liveness signal.
- No change to `cdcChunkingLockTTL` (still the lock's TTL), the read-path backstop, the
  placeholder/backing-less GC path (which keeps its interval-based `CreatedAtLT` cutoff),
  or `GetNar` re-download.
- No new config flags.

## Decisions

0. **Migration lock is the one liveness signal, applied at all three sites.** The 1h
   `cdcChunkingLockTTL` age was a pre-lock proxy for "is a chunker alive" used at read
   serve, write takeover, and cron reap. Since the download-path chunker now holds
   `migrationLockKey(hash)` for the whole chunk (`withNarMigrationLock`), the lock is a
   precise cross-instance liveness signal. Replace the age proxy with it everywhere.

1a. **Read path (the symptom fix).** `isServable`, for the ambiguous
   `total_chunks == 0 && chunking_started_at != NULL` case, probes liveness via a new
   `cdcChunkerLive` helper (non-blocking `TryLock` + immediate `Unlock`; no read-only
   "is held" query exists on `Locker`). Free lock → not servable → `GetNar` re-downloads
   *before* any bytes are streamed. Scoped to the ambiguous case so whole-file /
   fully-chunked serves never probe. On probe error, treat as live (fail safe).

1b. **Write path.** `findOrCreateNarFileForCDC` runs only while the caller holds the
   migration lock, so any in-progress row it sees is a dead orphan. Remove its `age <
   cdcChunkingLockTTL → ErrAlreadyExists` gate and always reclaim + take over. Also remove
   the now-redundant in-transaction age gate in `clearStaleCDCChunkingLockWithEntTx`
   (callers gate: read path pre-checks age, recovery + takeover use the lock).

1c. **Cron.** Drop the `staleCutoffTime` predicate on in-progress rows. The recovery
   candidate query keeps `total_chunks = 0 AND chunking_started_at IS NOT NULL`; the
   placeholder branch (`chunking_started_at IS NULL AND created_at < cutoffTime`) is
   unchanged. `recoverStaleCDCChunkingLock`'s per-row `TryLock(migrationLockKey(hash))`
   remains the gate: held → skip; free → reap.
2. **Special-case `ErrMigrationInProgress` in both background-chunking callers.** In the
   pipe path and simple-path goroutines, branch on `errors.Is(cdcErr, ErrMigrationInProgress)`:
   log at debug/info and return without `ds.setError`. All other errors keep the existing
   error-level log + `setError`.
3. **Add a distinct counter for lazy-disabled skips.** Introduce
   `lazyChunkingDisabledSkipCount`, increment it at the lazy-disabled branch instead of
   `staleRecoverySkipCount`, and emit it as a separate log field
   (`lazy_chunking_disabled_skip_count`).
4. **Correct the recovery doc comment** to state the lock-liveness premise (replacing the
   false "at startup there is no live in-process chunker" wording).

## Risks / Trade-offs

- **Lock TTL determines worst-case dead-orphan latency.** If a crashed chunker's lock is
  not released, it is reclaimable only after `cdcChunkingLockTTL` expires. Acceptable: the
  read-path stale-lock backstop already covers the interim, and this still beats the prior
  fixed 1h gate for the common case where the lock is released or expires sooner.
- **TryLock churn.** Recovery now `TryLock`s more candidates per run (no age pre-filter).
  Bounded by batch size and the keyset cursor; one cheap lock attempt per candidate.
- **Demoting `ErrMigrationInProgress` could mask a genuine stuck-lock.** Mitigated: the
  lock has a TTL and the recovery sweep independently reclaims orphans, so a truly stuck
  hash still heals.
