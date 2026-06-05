## Why

CDC drain mode exits automatically only when the chunked `nar_file` count reaches zero. Today the `migrate-chunks-to-nar` pass can leave NARs chunked forever, so drain never auto-completes and an operator must run manual SQL. Three failure classes were observed on a production cache draining CDC (kept as a real-corruption fixture rather than purged):

1. **~112 un-resolvable by the none URL**: a chunked `nar_file` whose only `nar_hash`-bearing narinfo advertises a *different-compression* URL (e.g. `nar/<hash>.nar.xz`) than the bare `nar/<hash>.nar`. The de-chunk verify-hash resolver only matches the exact Compression:none URL, so it can't find the NarHash and **skips** the NAR — it stays chunked.
2. **De-chunk re-breaks consistency**: de-chunking such a NAR to `none/whole` without normalizing its narinfo URL recreates the `url=.nar.xz` ≠ `none/whole` storage mismatch — serve-time normalization stops firing once the NAR is no longer chunked, so the `.nar.xz` URL 404s.
3. **~4 hard-fail reconstruction**: chunked NARs whose reconstruction fails outside the existing `ErrMissingChunk`/`ErrNarHashMismatch` purge paths are counted as `failed` and **left chunked**.

The net effect: a generic code gap — anyone who enables CDC and later drains it hits the same wall, not just this deployment.

## What Changes

- **(A) Resolve the verify NarHash by NAR hash, any narinfo URL** — not just the exact Compression:none URL — so the class-1 NARs become de-chunkable.
- **(B) Normalize the narinfo URL to Compression:none on successful de-chunk** so the de-chunked `none/whole` NAR and its narinfo stay consistent (no recreated `.nar.xz` 404).
- **(C) Purge-on-unverifiable**: when no NarHash is resolvable from any narinfo, or reconstruction hard-fails, **purge** the chunked `nar_file` (self-heal via upstream re-pull) instead of skipping/failing — so the pass always drives the chunked count to zero (drain self-completes).
- **(D) fsck repair pass** that detects chunked `nar_file` rows that are inconsistent or un-de-chunkable and relinks/normalizes/purges them — a safety net for anything the migration's purge-on-unverifiable did not reach.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `chunks-to-nar-migration`: the de-chunk pass MUST resolve the verify NarHash by NAR hash from any referencing narinfo, normalize the narinfo URL to none on de-chunk, and purge (not skip/fail) anything it cannot verify or reconstruct — so it always drives the chunked count to zero.
- `cdc-drain-mode`: drain MUST always be able to auto-complete after a full `migrate-chunks-to-nar` pass (no residue can strand it).
- `fsck`: fsck MUST detect and heal (relink/normalize/purge) inconsistent or un-de-chunkable chunked NARs.
- `cdc-chunking`: a de-chunked NAR's narinfo MUST advertise Compression:none consistent with its whole-file storage.

## Impact

- `pkg/cache/cache.go`: `MigrateChunksToNar` + `linkedNarinfoNarHash` (hash-based resolution, url normalization on de-chunk, purge-on-unverifiable).
- `pkg/ncps/migrate_chunks_to_nar.go`: driver loop error handling (purge the hard-fail class).
- `pkg/ncps/fsck.go`: a chunked-NAR residue repair pass.
- Validated against the real production 116 stragglers and by the CDC-lifecycle e2e tests.
