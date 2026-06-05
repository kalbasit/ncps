## Context

`migrate-chunks-to-nar` (`MigrateChunksToNar`) reconstructs each chunked NAR, verifies the assembled SHA-256 against the linked narinfo's NarHash (verified-or-nothing), writes the whole file, and flips `total_chunks` to 0. The verify hash is resolved by `linkedNarinfoNarHash`, which (after the reconcile-narinfo-narfile-links change) tries the join link, then falls back to the narinfo at the *exact* `nar/<H>.nar` URL. Production exposed three residue classes this still can't drain (see proposal). The driver loop (`pkg/ncps/migrate_chunks_to_nar.go`) purges on `ErrMissingChunk`/`ErrNarHashMismatch` but counts other errors as hard failures and exits non-zero, and the migration never normalizes narinfo URLs.

## Goals / Non-Goals

- **Goal**: a single de-chunk pass always reaches `chunked = 0`; de-chunked NARs are self-consistent. No operator SQL. (A complementary fsck reclaimer is a separate change.)
- **Non-Goal**: preserving every byte. A NAR we cannot content-verify is purged and re-pulled — re-pull yields clean, consistent storage, which is preferable to shipping an unverified or inconsistent NAR.

## Decisions

- **(A) Resolve NarHash by NAR hash, any URL.** Extend the resolver: after the join link and the exact-none-URL lookup miss, query narinfos referencing the NAR by hash (`url LIKE 'nar/<H>%'` or `nar_hash` present) and use the first with a non-null NarHash. The NarHash is the uncompressed NAR content hash and is identical regardless of the narinfo's advertised compression, so any referencing narinfo is a valid source. **This does not weaken verification:** de-chunk still reconstructs the NAR from chunks, computes the SHA-256 of the reconstructed bytes, and commits the whole file only if that digest equals the resolved NarHash — on mismatch it purges. So even if (A) resolved a wrong narinfo's NarHash, the reconstructed-content check would catch it (mismatch → purge), never persisting an unverified or wrong NAR. The reconstructed-content match is the gate; (A) only broadens where the *expected* hash is found.
- **(B) Normalize narinfo URL on de-chunk.** After the record flip, update every narinfo referencing the NAR to `nar/<H>.nar` / Compression none / FileHash null — persisting what `maybeCDCNormalizeNarInfoURL` only did at serve time while chunked. This closes the `url=.nar.xz` ≠ `none/whole` 404.
- **(C) Purge-on-unverifiable, not skip/fail.** When no NarHash is resolvable anywhere, purge (via the existing `PurgeChunkedNar` path) instead of `ErrNoNarHashToVerify`-skip; and broaden the driver loop so a hard reconstruction failure purges instead of counting `failed`. The pass thus always drains the row. Purge keeps the narinfo (re-pull self-heals).
- **fsck safety net is a separate change.** A daily-janitor fsck reclaimer (grace-period mark-then-purge, with a persistent mark column, gated so it never harms legitimately chunked NARs during active CDC) is proposed separately — it does not belong in the de-chunk migration, which already self-completes.

## Risks / Trade-offs

- Purge-on-unverifiable drops cached bytes for NARs we cannot verify. This is bounded (only un-verifiable/corrupt NARs) and self-heals on next access; the alternative (serving unverified or URL-inconsistent NARs) is worse.
- Normalizing narinfo URLs on de-chunk touches narinfo rows during the migration; it is idempotent and only applied to NARs actually de-chunked in the pass.

## Validation

Synthetic TDD fixtures for each class (different-compression-URL narinfo, no-NarHash, corrupt chunks). Then deployed and run against the **real production 116 stragglers** (kept un-purged precisely so the code is proven against real corruption), confirming `chunked → 0` and that the previously-stuck paths serve. The CDC-lifecycle e2e test exercises the full enable→drain→complete cycle including residue.
