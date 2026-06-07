## Why

When CDC chunking is enabled, the download path returns the NAR to the client before
the background chunker finishes. A crash (SIGKILL / OOM) mid-chunking leaves a `nar_file`
row in the orphan state (`total_chunks = 0`, `chunking_started_at` set, partial
`nar_file_chunks`). The read path treats the fresh lock as live, enters chunk streaming,
then stalls waiting for a chunk no producer will ever write — the client sees
`Truncated zstd input` (issue #1230). The current recovery only reaps such rows after a
1-hour stale age-gate, so the user-visible failure persists for up to an hour after every
unclean shutdown. The age-gate defeats the point of healing *fresh* orphans, and the
load-bearing safety premise ("at startup there is no live in-process chunker") is false in
multi-replica shared-DB deployments.

The unifying idea: the per-hash migration lock (`migrationLockKey(hash)`), which the
download-path chunker holds for the whole chunking operation, is the single
cross-instance liveness signal. It replaces the 1h `cdcChunkingLockTTL` age, which was
used as a (bad) liveness proxy in three places — read serve, write takeover, and cron
reap. A held lock = live chunker; a free lock = dead chunker, regardless of age.

- **Read path (the actual #1230 symptom fix).** `isServable` treats a fresh in-progress
  orphan (`total_chunks = 0`, recent `chunking_started_at`) as **not servable** when its
  migration lock is free, so `GetNar` re-downloads cleanly instead of entering
  chunk-serving and stalling `maxWaitPerChunk` on a chunk that never arrives
  (`Truncated zstd input`). A held lock keeps the row servable (legitimate streaming).
- **Write path.** `findOrCreateNarFileForCDC` (always entered while holding the migration
  lock) reclaims a prior in-progress orphan and re-chunks it **regardless of age**, rather
  than refusing a fresh row with `ErrAlreadyExists` — so the re-download above can actually
  re-chunk the orphan.
- **Cron.** The lazy-recovery sweep reaps orphaned mid-chunking rows of **any age**, gated
  solely on the migration-lock `TryLock` (skip if a peer holds it, reap if free). The 1h
  `staleCutoffTime` gate on in-progress rows is removed. This handles rows that are never
  re-requested and reclaims their chunk blobs.
- Background CDC chunking that fails with `ErrMigrationInProgress` (a peer holds the
  per-hash lock) is treated as a benign no-op: logged at debug/info, **not** at error
  level, and does **not** call `downloadState.setError`.
- The "lazy chunking disabled" skip is counted with a distinct counter, separate from the
  stale-lock-held skip counter, so `stale_recovery_skip_count` is not conflated.

## Capabilities

### New Capabilities
- (none)

### Modified Capabilities
- `cdc-chunking`: recovery liveness gating (lock-based, age-gate removed), benign
  `ErrMigrationInProgress` handling for background chunking, and recovery skip-counter
  disambiguation.

## Non-goals

- No change to the read path's own stale-lock backstop behavior or to `cdcChunkingLockTTL`
  as the TTL of the migration lock itself.
- No new config flags; recovery cadence remains the existing lazy-recovery cron schedule.
- No change to how completed chunking commits (`UpdateNarFileTotalChunks`) or to the
  on-demand `GetNar` re-download path that handles reverted rows.
- Not adding a heartbeat protocol; the existing per-hash migration lock is the liveness
  signal.

## Impact

- Code: `pkg/cache/cache.go` — `isServable` + new `cdcChunkerLive` probe (read-path
  liveness gate), `findOrCreateNarFileForCDC` (age-free orphan takeover under the lock),
  `clearStaleCDCChunkingLockWithEntTx` (age gate removed), `runCDCLazyRecovery` (recovery
  query predicate + skip counters), and the background-chunking error handling in the
  download paths (`reportBackgroundCDCError`).
- I/O / latency: orphaned rows are reclaimed within one cron interval (~5m) instead of up
  to 1h; no change to the hot read/write path. Recovery still reads in batches and does
  one `TryLock` + one transaction per candidate row, so steady-state I/O is unchanged.
- Logs/observability: fewer error-level entries in HA fleets (benign lock contention
  demoted to debug/info); a new skip counter field distinguishes lazy-disabled skips from
  stale-lock-held skips.
