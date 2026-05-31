## Context

`isCDCEnabled()` in `pkg/cache/cache.go` returns `c.cdcEnabled && c.chunkStore != nil`.
It is used as a single gate for **both writes and reads**: whether to store a new NAR
as chunks (write) and whether to serve an existing NAR from the chunk store (read).
`SetChunkStore` is only called from `serve.go` when `cdcEnabled=true`, so setting
`cdc.enabled: false` leaves `chunkStore == nil`, making every call to `isCDCEnabled()`
return false — including the read paths that check for existing chunks.

Affected read-path call sites:
- `isServable` (line 3900): short-circuits to `false` when `!isCDCEnabled()`,
  so chunked NARs are never considered servable → cache miss → upstream re-fetch.
- `HasNarInChunks` (line 6994): same guard, returns `false` unconditionally.
- `GetNarInfo` normalization (line 1585): skips the in-memory chunk normalization.

PR #1304 (`allow-disabling-cdc`) introduced `DeleteCDCConfig` which clears
`cdc_enabled` from the DB at startup when `cdc.enabled: false`. This breaks
`migrate-chunks-to-nar`, which checks `cdc_enabled=true` in the DB before proceeding.

## Goals / Non-Goals

**Goals:**
- Separate the chunk-write gate from the chunk-read gate so chunked NARs continue
  to serve during the drain period.
- Initialize the chunk store in read-only (drain) mode when `cdc.enabled: false`
  but `cdc_enabled=true` is still in the DB (i.e. chunks may exist).
- Preserve the DB config during drain so `migrate-chunks-to-nar` can run concurrently
  with a CDC-disabled server.
- Revert the `DeleteCDCConfig` call from PR #1304.
- Remove the `cdc_enabled` DB check from `migrate-chunks-to-nar`; use chunk count instead.

**Non-Goals:**
- Automatic background drain job (tracked separately).
- Auto-clearing DB config when drain completes (tracked separately).
- Enforcing chunk-store write prohibition at the storage layer (cache layer is sufficient).
- Changing the chunk-store flags or configuration surface.

## Decisions

### 1. Split `isCDCEnabled()` into write-gate and read-gate

**Decision**: Add `isChunkStoreAvailable() bool { return c.chunkStore != nil }` for
reads. Keep `isCDCEnabled()` for writes (unchanged semantics: `c.cdcEnabled && c.chunkStore != nil`).

Change ~3 read-path call sites from `isCDCEnabled()` to `isChunkStoreAvailable()`:
- `isServable`: the CDC branch that queries `nar_file` and calls `narFileServable`
- `HasNarInChunks`: the early-return guard
- `GetNarInfo` normalization: the in-memory rewrite of `Compression`/`URL`

All ~22 write-path call sites (`putNarWithCDC`, `storeNarWithCDC`,
`maybeBackgroundMigrateNarToChunks`, lazy-chunking paths, etc.) remain gated on
`isCDCEnabled()` — they must NOT fire during drain.

**Why**: Targeted change with minimal blast radius. Adds one small helper; all existing
write-path logic is untouched. The split is the minimal expression of "reads from
chunks are independent of whether new chunks are being written."

**Alternative considered**: Rename all `isCDCEnabled()` call sites to either
`isChunkWriteEnabled()` or `isChunkStoreAvailable()`. Rejected: too broad a rename
with high merge-conflict risk; the targeted 3-site change is safer.

### 2. Initialize chunk store in drain mode from `serve.go`

**Decision**: In `createCache`, after `ValidateOrStoreCDCConfig` returns nil for the
enabled→disabled case, check whether the stored DB config had `cdc_enabled=true`
(using the `storedWasEnabled` value obtained before the validate call). If drain mode
is detected (`storedWasEnabled && !cdcEnabled`), call `getChunkStorageBackend` and
`c.SetChunkStore` even though `cdcEnabled=false`. Do NOT call `SetCDCConfiguration`
with `enabled=true` — the chunker is not created, `c.cdcEnabled` stays false.

**Why**: The chunk store is a pure I/O backend; initializing it for reads when writes
are disabled is safe. The gate against new-chunk writes remains `isCDCEnabled()`, which
returns false because `c.cdcEnabled=false`.

**Alternative considered**: Add a dedicated `SetDrainMode` method to `Cache`. Rejected:
not needed — `SetChunkStore` already sets the store; the write gate is already correct;
no new state required.

### 3. Revert `DeleteCDCConfig` from PR #1304; auto-clear DB config when drain completes

**Decision**: In `validateCDCConfig`, the `storedEnabled && !enabled` branch returns
`nil` without calling `DeleteCDCConfig`. The four DB config keys (`cdc_enabled`,
`cdc_min`, `cdc_avg`, `cdc_max`) are left intact during the drain period so that
`migrate-chunks-to-nar` can proceed concurrently and the chunk store can be initialized
on every restart.

The stored config is cleared automatically at startup when drain mode is entered AND
the chunk count is zero — meaning all NARs have already been migrated. In that case
the startup code calls `DeleteCDCConfig`, logs "CDC drain complete, stored config
cleared", does NOT initialize the chunk store, and starts in fully-disabled mode. This
is the only path that auto-clears the config.

This produces a clean state machine:

| DB `cdc_enabled` | config `cdc.enabled` | chunks remain | Mode |
|---|---|---|---|
| absent | false | — | fully disabled |
| true | true | — | full CDC (read + write) |
| true | false | yes | drain (read chunks, write whole) |
| true | false | no | auto-clears config → fully disabled |

`DeleteCDCConfig` is kept in `pkg/config/config.go`; the `validateCDCConfig` call
to it is removed (that was the PR #1304 mistake). The function is now called from
`serve.go` when drain-complete is detected.

**Why**: Without auto-clear, `cdc_enabled=true` persists in the DB indefinitely —
the chunk store is initialized on every restart forever, even years after the last
chunk was migrated. Auto-clear at startup (on zero-chunk detection) makes the
transition self-completing without any operator command. The check is a single count
query and is only evaluated when drain mode is active.

### 4. Remove `cdc_enabled` DB check from `migrate-chunks-to-nar`

**Decision**: Remove the early check in `migrateChunksToNarCommand` that fails when
`cdc_enabled != "true"`. Replace it with a count query: if
`dbClient.Ent().NarFile.Query().Where(entnarfile.TotalChunksGT(0)).Count(ctx) == 0`,
print "nothing to migrate" and exit 0. Otherwise proceed normally.

**Why**: The `cdc_enabled` flag is a write-config concern, not a data-existence concern.
During drain mode `cdc_enabled` remains `"true"` in the DB anyway, so this check would
pass — but coupling the migration command to that flag is fragile. The authoritative
signal is whether chunked NAR records exist.

## Risks / Trade-offs

- **PR ordering**: This change supersedes PR #1304. PR #1304 must be closed or its
  `DeleteCDCConfig` call reverted before or alongside merging this change. Merging #1304
  first and this second is safe; the revert is a no-op locally.
- **Drain mode detection is per-restart**: Each `serve` startup queries the DB to detect
  drain mode. If the DB is unavailable at startup, drain mode cannot be detected —
  same failure mode as all DB-dependent startup logic.
- **Chunk store flags required in drain mode**: Operators must still supply
  `--cache-storage-local` (or S3 flags) matching the original chunk storage when running
  in drain mode. The chunk store configuration does not change between CDC-on and drain.
- **`maybeBackgroundMigrateNarToChunks` correctly disabled in drain**: It is gated on
  `isCDCEnabled()` (write gate), so it will not push whole-file NARs into chunks during
  drain. Correct.

## Migration Plan

1. Merge this change (supersedes / closes PR #1304).
2. Operator sets `cdc.enabled: false` and deploys — drain mode activates automatically.
3. Operator runs `migrate-chunks-to-nar` concurrently (or afterward) at their own pace.
4. Chunked NARs are served from the chunk store until migrated.
5. Once migration completes (`total_chunks=0` for all rows), the DB config can be
   cleared manually if desired (operator command or future feature).
