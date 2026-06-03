## Context

The CDC→whole-file migration has processed ~45k NARs. 168 remain permanently stuck: their chunk data consistently produces a wrong hash on reconstruction across every run. `cache.MigrateChunksToNar` returns `ErrNarHashMismatch` for each; the command counts those as `totalFailed`, and exits non-zero via `ErrChunksToNarFailures`. The Kubernetes Job therefore stays in `Failed` state even though 99.6% of the work is complete and the broken 168 can never migrate — they will always mismatch.

These entries are unserveable today: `GetNar` would also fail to reconstruct them. Purging them allows the normal cache-miss path to re-fetch them fresh from upstream.

## Goals / Non-Goals

**Goals:**
- Exit 0 after a complete run where every NAR was either migrated or purged
- Purge permanently broken chunked NARs (hash/size mismatch; missing chunk) inline during migration so they re-enter the normal fetch path

**Non-Goals:**
- Root-causing why 168 NARs have corrupt chunk data
- Changing behavior for transient errors (I/O failures, lock errors remain counted as failures → non-zero exit)
- Schema or SQL changes
- Changing the normal `GetNar` / `GetNarInfo` serve path

## Decisions

### Add `PurgeChunkedNar(ctx, narURL)` to `Cache`

Keep purge logic in the cache package co-located with `MigrateChunksToNar`. The command calls `PurgeChunkedNar` when `MigrateChunksToNar` returns `ErrNarHashMismatch`. This mirrors the existing pattern where the command calls cache methods and the cache package owns DB + store operations.

Considered alternative: handle purge directly in the command using raw `dbClient` calls. Rejected: breaks encapsulation; the command already doesn't know about internal DB schema details.

### Purge sequence: links + record in one transaction, then orphaned chunk objects

`PurgeChunkedNar` deletes `nar_file_chunks` links and the `nar_file` record together in a single database transaction. After that transaction commits, orphaned chunk objects (chunk rows with no remaining `nar_file_chunks` links) are reclaimed from the chunk store. This ordering means the `nar_file` is atomically removed before any chunk objects are touched, so a concurrent `GetNar` that races the purge sees no `nar_file` and falls through to the upstream fetch rather than attempting to serve from a partially-deleted chunk set.

Chunk object deletion is dedup-safe: the delete is conditioned on `!HasNarFileLinks()` at delete time, so a chunk that was re-linked by a concurrent re-fetch between the orphan snapshot and the delete is not removed.

Unlike the normal migration path, the purge always reclaims chunk objects immediately (no `--force-reclaim` gating): the NAR was unserveable, so there is no valid in-flight chunk-serve that could be truncated.

`PurgeChunkedNar` acquires the same migration lock key as `MigrateChunksToNar` to prevent a concurrent migration from racing the purge. If the lock is already held by another worker, the purge returns `ErrMigrationInProgress` and the command treats it as a skip (the other worker is handling it).

### Narinfo record is left intact

The narinfo describes the NAR metadata (store path, references, compression) independently of whether the bytes are cached. Leaving the narinfo means the next `GetNarInfo` succeeds and returns the path info; the subsequent `GetNar` finds no `nar_file` and falls through to the upstream fetch, which re-downloads and re-stores correctly. Deleting the narinfo would be unnecessarily destructive and break the normal cache-miss recovery path.

### New `totalPurged` counter; exit 0 only when `failed == 0`

Add an `int32` atomic `totalPurged` counter alongside the existing `totalFailed`/`totalSucceeded`. The final summary log line includes `purged`. The command exits 0 when `failed == 0`, regardless of `purged`. Only transient/unexpected errors (non-`ErrNarHashMismatch`, non-`ErrMissingChunk`) increment `totalFailed` and drive a non-zero exit.

### Both `ErrNarHashMismatch` and `ErrMissingChunk` trigger purge

Hash/size mismatch (`ErrNarHashMismatch`) and absent chunk objects/DB entries (`ErrMissingChunk`) are both deterministic, data-level failures that will never self-heal. `MigrateChunksToNar` detects missing chunks via `chunk.ErrNotFound` or `storage.ErrNotFound` from `getNarFromChunks` and wraps them as `ErrMissingChunk`. Other errors (I/O timeouts, lock acquisition failures, query errors) may be transient and remain counted as failures.

## Risks / Trade-offs

- **Upstream no longer has the NAR** → The NAR was already permanently unserveable in ncps. If the upstream also lacks it, the re-fetch 404s — same outcome as today, but the broken chunked entry is gone. Mitigation: acceptable; the upstream (e.g. cache.nixos.org) retains NARs indefinitely for active paths.
- **Purge races a concurrent chunk-serve** → The migration lock (held by `PurgeChunkedNar`) prevents concurrent migration. A concurrent chunk-serve reading the chunks of a hash-mismatching NAR would already be serving corrupt bytes; purging while it reads is no worse. Mitigation: the lock serialises migration-path access; serve-path readers are unaffected by normal GC and accept eventual consistency.
- **Exit 0 masks purge events** → Monitoring that checks only job exit code sees success. Mitigation: `purged` count appears in the final summary log line; a Loki alert on `purged > 0` can be configured separately.

## Migration Plan

1. Add `PurgeChunkedNar` method to `Cache` in `pkg/cache/cache.go` (+ export via `pkg/ncps/export_test.go` if needed for tests).
2. Add `ErrNarHashMismatch` branch in `migrateChunksToNarAction` that calls `c.PurgeChunkedNar` and increments `totalPurged`.
3. Change the exit condition from `failed > 0` to `failed > 0` only (purged entries do not set `failed`).
4. Update progress ticker and summary log to include `purged`.
5. Write TDD tests: hash-mismatch NAR is purged; nar_file record gone; chunk objects gone; narinfo intact; exit 0.
6. No DB schema changes, no migration files needed.
