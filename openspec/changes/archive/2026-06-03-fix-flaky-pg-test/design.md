## Context

`GetNar` (`pkg/cache/cache.go`, ~line 1085) reads store presence into a local
`hasNarInStore` once, then determines servability through `isServable`, which
performs its **own** fresh `HasNarInStore` call. These are two distinct
time-of-check observations of the same store:

```go
hasNarInStore := c.HasNarInStore(ctx, narURL)      // (1) may observe absent
...
hasNar, err := c.isServable(ctx, narURL)           // (2) calls HasNarInStore again — may observe present
...
if hasNar {
    size, reader, err = c.serveNarFromStorageViaPipe(ctx, &narURL, hasNarInStore) // stale (1) passed in
}
```

`isServable` short-circuits to `false` when `!c.isChunkStoreAvailable()` and the
whole file is absent, so `hasNar=true` with `hasNarInStore=false` is only
reachable when the whole file **landed between (1) and (2)** — `isServable`
observed it present. The stale `hasNarInStore=false` is then handed to
`serveNarFromStorageViaPipe`, where:

```go
serveFromChunks := !hasInStore        // = true (stale)
...
if serveFromChunks { c.getNarFromChunks(...) }   // → "chunk store not initialized" when no chunk store
```

`getNarFromChunks` guards on `isChunkStoreAvailable()` and returns
`chunk store not initialized, cannot serve NAR from chunks: <ErrNotFound>`. In
the flaky `RunLRUCleanupInconsistentNarInfoState` test (CDC-disabled variant,
chunk store nil), this surfaces as `require.NoError` failing at idx 5.

This is the inverse of #1324: that fix handled present-at-check then
deleted-before-use (fall back store→chunks). This is absent-at-check then
present-before-use, mis-routed to a non-existent chunk store.

## Goals / Non-Goals

**Goals:**
- `GetNar` serves a whole-file NAR that is present at serve time even when an
  earlier check observed it absent.
- The uncompressed serve path never calls `getNarFromChunks` when no chunk store
  is configured.
- Deterministic regression test for the stale-`hasInStore` race.
- Fix passes under `-race`; no change to the chunk-store-present behavior.

**Non-Goals:**
- Reworking CDC serve/migration architecture or the #1324 fallback.
- Changing LRU cleanup logic.
- Touching the compressed-request chunk rules.

## Decisions

### Decision 1: Re-evaluate store presence inside `serveNarFromStorageViaPipe` instead of trusting the passed `hasInStore`

The `hasInStore` parameter is a stale snapshot from `GetNar`. The most robust fix
is to make the serve path's chunk-routing decision depend on currently-true
conditions rather than the stale flag:

- Default the chunk route on chunk-store availability: only set
  `serveFromChunks := !hasInStore` to route to chunks when
  `c.isChunkStoreAvailable()` is true. When no chunk store exists, an uncompressed
  request resolves against the whole-file store regardless of the stale flag.
- Because `getNarFromStore` returns `ErrNotFound` cleanly when the whole file is
  truly absent, and the existing #1324 fallback already handles the
  store-miss→chunks case **when a chunk store exists**, gating the chunk route on
  `isChunkStoreAvailable()` is sufficient and symmetric with that fallback.

**Alternatives considered:**
- *Refresh `hasNarInStore` in `GetNar` right before the serve call.* Narrower, but
  leaves `serveNarFromStorageViaPipe` trusting a caller-supplied flag that can
  still go stale under the read lock; the serve function is the correct authority
  for its own routing. Rejected in favor of fixing it at the routing site.
- *Make `getNarFromChunks` fall back to the store on "chunk store unavailable".*
  Inverts the layering (chunk path reaching back into store path) and muddies the
  #1324 store→chunks fallback. Rejected.

### Decision 2: Keep `storage.ErrNotFound` semantics intact

When the whole file is genuinely absent and no chunk store exists, the serve path
still returns `storage.ErrNotFound`, preserving cache-miss recovery (upstream
re-download) for normal mode and the upload-only `ErrNotFound` contract. The fix
only prevents the *spurious* `chunk store not initialized` error for a NAR whose
whole file is actually present.

### Decision 3: TDD with a deterministic race reproduction

Following the #1324 precedent (`cdc_migration_race_internal_test.go`), reproduce
the stale-`hasInStore` window deterministically rather than relying on timing.
Options: a `narStore` wrapper whose `HasNar`/stat transitions from absent→present
across calls, or driving `serveNarFromStorageViaPipe` directly with
`hasInStore=false` while the whole file is present and chunk store is nil. The
direct-serve approach is the tightest RED for the exact CI error.

## Risks / Trade-offs

- **[Masking a genuine chunk-only NAR served with stale flag]** → Not possible:
  the chunk route is only skipped when `isChunkStoreAvailable()` is false; when a
  chunk store exists, behavior (including the #1324 fallback) is unchanged.
- **[Extra store stat on the racing path]** → Negligible; only on the narrow
  `!hasInStore` uncompressed branch, and the whole-file stat is the same call the
  store-serve path makes anyway.
- **[Regression test flakiness]** → Mitigated by a deterministic wrapper/direct
  call; assert under `-race` with repeated runs.

## Migration Plan

Pure in-process serve-path logic change. No schema, config, or API changes; no
data migration. Rollback is reverting the commit. Safe under expand-contract (no
DDL).

## Open Questions

- Final RED harness shape (store wrapper vs direct `serveNarFromStorageViaPipe`
  call) — to be settled during TDD; the spec scenarios are the acceptance bar
  either way.
