## Context

`nix copy --to .../upload` aborts with `cannot add 'X' because the reference 'Y' does not exist`. Root cause confirmed in prod (see `project_nar_serving_failures_topology` memory): the substituter/root narinfo read path calls `purgeNarInfo` when the backing NAR is momentarily absent (NFS local backend) or evicted. Because prod shares one Postgres across 2 non-sticky replicas, the delete is globally visible instantly, flipping a concurrently-verified reference `200 -> 404` mid-copy.

Two read sub-paths purge on a missing NAR:
- `getNarInfoFromDatabase` — `pkg/cache/cache.go:4435` (DB-sourced narinfo).
- `getNarInfoFromStore` — `pkg/cache/cache.go:4169` (store-sourced narinfo).

A third call, `:4136`, purges a *corrupt/unparseable* narinfo (different fault).

`purgeNarInfo` (`:4580`) deletes the narinfo, then the `nar_file` record (`:4604`) **and** the NAR bytes (`:4629`) keyed only by `(hash, compression, query)` — with no link check. Since `narinfo`↔`nar_file` is M:N (`NarInfoNarFile` join), this can delete a present, still-shared NAR and break sibling narinfos.

Key existing facts the design leans on:
- The upload-only path (`:4427`) already returns `errNarInfoPurged` **without** purging — proof the non-destructive shape works.
- After an upstream pull, the re-read maps `errNarInfoPurged -> storage.ErrNotFound -> 404` (`:3523`), so leaving records intact on an upstream miss yields a correct 404 with no loop.
- `RunLRU` already deletes `nar_file`s only when orphaned (`Not(HasNarInfoNarFiles())`, `:6243-6267`) — the reference implementation for refcount-safe deletion.

## Goals / Non-Goals

**Goals:**
- Make the substituter (non-upload) missing-NAR read path non-destructive: re-fetch from upstream and overwrite in place; never delete narinfo/`nar_file`/bytes as part of a read.
- Make `purgeNarInfo` refcount-aware: never delete a `nar_file`/NAR bytes still linked to another narinfo.
- Preserve all existing client-visible HTTP outcomes (200 upstream-hit, 404 upstream-miss, never 500) and the upload-only non-destructive behavior.

**Non-Goals:**
- LRU eviction (already refcount-safe).
- Topology change (object-store backend / single replica) — separate effort.
- Renaming the `errNarInfoPurged` sentinel (its meaning shifts to "missing-backing miss"; rename is churn, deferred).

## Decisions

1. **Drop the `purgeNarInfo` call in the two missing-NAR read paths** (`:4169`, `:4435`); return `errNarInfoPurged` directly. The existing upstream-fetch fall-through (non-upload) and `ErrNotFound` mapping (upload + upstream-miss re-read) already produce the correct results. This is the minimal change that removes both the destructive cross-replica window and the collateral shared-NAR deletion in one stroke.
   - The upload-only special-case at `:4427` becomes redundant (both branches now return `errNarInfoPurged` without purging); collapse it so DB and store paths share one non-destructive return.
2. **Make `purgeNarInfo` refcount-safe** for its remaining caller (`:4136`, corrupt narinfo): inside the transaction, after deleting the narinfo row, delete the `nar_file` record + NAR bytes **only if** no other `NarInfoNarFile` link to that `nar_file` remains. Mirror LRU's `Not(HasNarInfoNarFiles())` predicate. (For `:4136` the parsed `narURL.Hash` is typically empty so bytes are already untouched; this is defense-in-depth and satisfies the refcount requirement.)
3. **Healing is via upsert, not delete-then-recreate.** Confirm (via test) the upstream-fetch narinfo upsert overwrites an existing stale narinfo's fields, so leaving the record intact does not pin stale data.

## Risks / Trade-offs

- **Stale-record persistence on upstream miss.** Leaving a missing-NAR narinfo record means a hash that is genuinely gone upstream keeps a record returning 404. This matches the already-shipped upload-only behavior and `nar-cache-miss-recovery` ("no destructive negative-caching"); it is idempotent and self-heals on a later PUT or upstream availability. Accepted.
- **`purgeNarInfo` refcount check adds one indexed link lookup** on the rare corrupt-narinfo path. Negligible.
- **Spec/test churn:** existing `narinfo-purge-serving` scenarios asserting a destructive root purge must be updated to the non-destructive behavior. Covered by the delta spec; existing tests updated under TDD (not duplicated).
- **Behavior parity across backends:** the change is pure Go cache logic (no SQL/schema/migration), so SQLite/Postgres/MySQL behave identically; no migration needed.
