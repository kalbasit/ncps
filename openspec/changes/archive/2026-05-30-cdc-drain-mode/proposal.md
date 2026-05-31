## Why

Turning CDC off is not symmetric with turning it on. When CDC is enabled on a
cluster that has whole-file NARs, those whole files keep serving normally while
operators optionally run `migrate-nar-to-chunks` in the background — the
`cdcEnabled` flag only gates the write path; reads stay independent.

When CDC is disabled, the opposite should hold: new NARs go to whole-file
storage immediately, but existing chunked NARs should continue to serve from
the chunk store while `migrate-chunks-to-nar` drains them in the background.
Instead, the current implementation treats disable as a hard cutover:

1. The chunk store is never initialized, so all chunked NARs become cache misses
   (upstream re-fetches) the moment the new binary starts — even mid-transfer.
2. `allow-disabling-cdc` (PR #1304) clears `cdc_enabled` from the DB at startup,
   so running `migrate-chunks-to-nar` in parallel immediately fails ("CDC was
   never enabled").
3. Large deployments with millions of NARs can never reach zero chunked NARs
   before the operator needs to disable CDC — a graceful drain is the only
   viable operational path.

## What Changes

- **Separate write gate from read gate**: `cdcEnabled` continues to gate the
  write path only. Chunk reads are gated solely on whether the chunk store is
  initialized (`chunkStore != nil`).
- **Drain mode**: When `cdc.enabled: false` but `cdc_enabled=true` is still in
  the DB (i.e. chunked NARs may exist), initialize the chunk store in read-only
  mode so existing chunks can be served without writing new ones.
- **Preserve DB config while draining**: Do not clear `cdc_enabled` from the DB
  at startup. Clear it only when the operator's intent is verified complete
  (zero chunked NARs remain) or explicitly forced. This allows
  `migrate-chunks-to-nar` to run concurrently with a CDC-disabled ncps.
- **Revert the `DeleteCDCConfig` call from `allow-disabling-cdc`**: The startup
  clear introduced in PR #1304 is the root cause; drain mode supersedes it.
- **Optional background drain job**: When drain mode is active, optionally start
  a background worker (analogous to the lazy-chunking recovery job) that calls
  `migrate-chunks-to-nar` internally on a schedule, so the cluster self-heals
  without requiring a separate operator step.

## Capabilities

### New Capabilities

- `cdc-drain-mode`: Describes the read-only chunk-serving state entered when
  `cdc.enabled: false` but chunked NARs still exist in the database. Covers
  write-gate/read-gate separation, DB config lifecycle, concurrency with
  `migrate-chunks-to-nar`, and completion detection (transition to fully
  disabled once no chunks remain).

### Modified Capabilities

- `cdc-chunking`: CDC startup validation lifecycle changes — the
  enabled→disabled transition no longer clears DB config; drain mode is entered
  instead. The `cdcEnabled` runtime flag only controls the write path.
- `cdc-disable`: Replaces the hard-cutover semantics with drain-mode semantics;
  the "chunked NARs become cache misses" requirement is removed.

## Impact

- `pkg/config/config.go`: Remove the `DeleteCDCConfig` call from
  `validateCDCConfig`; the enabled→disabled case just returns nil and leaves
  stored config intact.
- `pkg/ncps/serve.go`: Initialize chunk store even when `cdcEnabled=false` if
  stored DB config has `cdc_enabled=true`. Rename / introduce a
  `cdcDraining` concept to distinguish write-disabled-but-read-active state.
- `pkg/cache/cache.go`: Split `isCDCEnabled()` into write-gate
  (`cdcEnabled && chunkStore != nil`) and read-gate (`chunkStore != nil`); all
  chunk read paths use the read gate.
- `pkg/ncps/migrate_chunks_to_nar.go`: Remove the hard fail on
  `cdc_enabled != "true"` — if chunked NARs exist in the DB, the migration
  should proceed regardless of the stored enabled flag.
- No schema changes. No new CLI flags required (drain mode is automatic).
