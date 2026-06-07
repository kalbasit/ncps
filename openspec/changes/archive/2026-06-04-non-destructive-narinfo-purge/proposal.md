## Why

`nix copy --to .../upload` still aborts in production with `cannot add 'X' because the reference 'Y' does not exist`, with every prior fix (#1321/#1323/#1324/#1325) deployed. Root cause confirmed live: the **substituter/root** narinfo read path still calls `purgeNarInfo` when a backing NAR is absent (`pkg/cache/cache.go:4136/4169/4435`). Because production shares one Postgres across replicas, that delete is globally visible the instant it fires. A NAR that is transiently un-stat-able on the NFS local backend (or evicted by LRU) makes a reference read `200` then `404` within a single copy — and Lix aborts. #1321 only made the `/upload` path non-destructive; the root path was intentionally left destructive.

## What Changes

- Make the non-upload (root/substituter) narinfo read path **non-destructive**: when the purge guard's missing-NAR condition is met, re-fetch from upstream and overwrite/heal the record **without first deleting it**. The record is mutated only by the upstream outcome (success overwrites it; genuine upstream-404 leaves it for a later PUT/re-fetch to heal), eliminating the window where a concurrent reader sees the record vanish.
- **Make NAR deletion refcount-aware.** `narinfo`↔`nar_file` is M:N (`NarInfoNarFile` join; n:1 in practice — many narinfos share one NAR). Today `purgeNarInfo` (`pkg/cache/cache.go:4604`/`:4629`) deletes the `nar_file` record **and the NAR bytes** keyed only by `(hash, compression, query)`, with no link check — so a false-positive purge destroys a present, still-shared NAR and breaks sibling narinfos. Any narinfo-driven NAR deletion MUST delete the NAR only when it is truly orphaned (zero remaining `NarInfoNarFile` links), mirroring how `RunLRU` already does it (`entnarfile.Not(entnarfile.HasNarInfoNarFiles())`).
- **BREAKING (spec-level, not API):** supersedes the existing requirement that the root path purges-then-re-fetches. Client-visible HTTP behavior is unchanged (200 on upstream hit, 404 on upstream miss); only the destructive delete window and the cross-narinfo collateral deletion are removed.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `narinfo-purge-serving`:
  1. The non-upload (root/substituter) read path MUST re-fetch from upstream **without a destructive delete window**, instead of the current purge-then-re-fetch; the record is never transiently absent to a concurrent reader.
  2. Any narinfo-driven NAR/`nar_file` deletion MUST be **refcount-aware**: it MUST NOT delete a `nar_file` record or its NAR bytes while another narinfo still links to that `nar_file`.

## Non-goals

- **LRU eviction is out of scope** — `RunLRU` is already refcount-safe (orphan-only `nar_file` deletion); it is not a phantom source.
- The topology fix (moving the NAR backend off shared-NFS-local to an object store, or single-replica). Tracked separately; this change reduces the blast radius but does not require it.
- The `/upload` path (already non-destructive via #1321) and the NAR-HEAD-verifies-bytes fix (#1323).
- Replica stickiness / load-balancer changes.

## Impact

- **Code:** `pkg/cache` narinfo read path (`getNarInfoFromDatabase` / `GetNarInfo` purge guard) and `purgeNarInfo` (refcount guard); `pkg/server` handler unchanged (already maps the sentinel to 404). `RunLRU` untouched.
- **I/O / latency:** root reads on a missing-NAR narinfo do one fewer write (no DELETE) before the existing upstream fetch; net latency unchanged or slightly lower. A refcount check adds at most one indexed link lookup on the rare purge path.
- **Memory:** none.
- **Concurrency:** removes a cross-replica destructive race against in-flight `nix copy` reference verification.
