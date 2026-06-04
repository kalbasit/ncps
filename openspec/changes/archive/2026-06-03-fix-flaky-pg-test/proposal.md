## Why

`TestCacheBackends/.../RunLRUCleanupInconsistentNarInfoState` flakes in CI
([run 26929993682](https://github.com/kalbasit/ncps/actions/runs/26929993682/job/79447676107))
with `chunk store not initialized, cannot serve NAR from chunks`. The root cause
is a time-of-check/time-of-use (TOCTOU) race on the `hasInStore` flag in
`GetNar`: the whole-file NAR is observed **absent** at one check, lands in the
store moments later, and the now-stale "absent" flag makes the serve path route
an uncompressed request to the chunk store — which hard-fails when no chunk store
is configured. This is the inverse of the #1324 fix (present-at-check then
deleted); here it is absent-at-check then present.

## What Changes

- `GetNar` (`pkg/cache/cache.go`) computes `hasNarInStore` once (line ~1090),
  then `isServable` re-checks store presence with a fresh `HasNarInStore`. When
  the whole-file lands between those two checks, `isServable` returns
  `hasNar=true` while the stale `hasNarInStore=false` is passed into
  `serveNarFromStorageViaPipe`.
- In `serveNarFromStorageViaPipe`, `serveFromChunks := !hasInStore` then routes
  to `getNarFromChunks`, which returns `chunk store not initialized` when no
  chunk store exists.
- **Fix**: the serve path MUST NOT route an uncompressed request to the chunk
  store when no chunk store is available. When the whole file is in fact present
  (or a chunk store is absent), it MUST (re)serve from the whole-file store; it
  may only return `storage.ErrNotFound` when the whole file is genuinely absent.
  Concretely, eliminate reliance on the stale `hasInStore` by re-evaluating store
  presence at serve time (or gating the chunk route on chunk-store availability).
- Add a deterministic regression test reproducing the stale-`hasInStore` race.
- No **BREAKING** changes.

## Capabilities

- **New Capabilities**: none.
- **Modified Capabilities**:
  - `cdc-chunking` — adds a sibling to the existing "serving a whole-file NAR
    MUST be resilient to a concurrent background migration" requirement, covering
    the inverse TOCTOU: a whole-file observed absent then present MUST still be
    served, and the serve path MUST NOT attempt a chunk serve when no chunk store
    is configured.

## Impact

- **Code**: `pkg/cache/cache.go` (`serveNarFromStorageViaPipe` `serveFromChunks`
  decision and/or the `hasNarInStore` value threaded from `GetNar`); new
  regression coverage in `pkg/cache/*_internal_test.go`.
- **Behavior**: A NAR whose whole-file lands during the serve decision is served
  successfully instead of erroring with `chunk store not initialized`. Removes
  the flaky CI failure. Non-CDC deployments are unaffected by chunk-store errors
  on the read path.
- **I/O / network / memory**: Negligible. At most one additional `HasNarInStore`
  stat on a narrow, already-racing path; no new allocations, DB queries, or
  network calls on the common (whole-file present at first check) path.

## Non-goals

- Reworking the broader CDC serve/migration architecture or the #1324
  store→chunks migration-race fallback.
- Changing chunked-serving behavior when a chunk store **is** configured.
- Altering LRU cleanup logic — the failing test merely exposes the serve-path
  race; the fix targets the serve path, not LRU.
