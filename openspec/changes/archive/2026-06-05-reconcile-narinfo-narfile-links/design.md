## Context

The `narinfo_nar_files` join table is the canonical narinfo↔nar_file relationship (used by refcount-safe purge, LRU, and de-chunk hash resolution). It is created in `storeInDatabase` (the narinfo-write path) via `createOrUpdateNarFileEnt` + `NarInfoNarFile.Create`. Under CDC, the NAR is downloaded and chunked **asynchronously** by `storeNarWithCDC`, which finalizes the chunked `nar_file` row (`SetTotalChunks`) independently of the narinfo write. A timing race between these two paths leaves a fraction (~0.8% observed) of chunked `nar_file` rows with no join link.

Two downstream consumers fail closed on the missing link:
- `linkedNarinfoNarHash` (de-chunk) resolves the verification NarHash **only** via the join; with no link it returns nil and `MigrateChunksToNar` skips the NAR (`ErrNoNarHashToVerify`). The chunked set never reaches zero, so drain mode never exits.
- `repairFsckIssues` treats an unlinked narinfo as an orphan and **deletes** it — destroying valid, reachable metadata.

## Goals / Non-Goals

- **Goal**: make the system resilient to a missing link (de-chunk still drains; fsck repairs not deletes) and close the race at the source (reconcile on chunk completion).
- **Non-Goal**: re-architecting link creation to be transactional with chunking. The reconcile-on-completion approach is sufficient and low-risk; the resilience fixes cover any link gap regardless of cause.

## Decisions

- **De-chunk fallback by URL, not by re-deriving the link eagerly.** `linkedNarinfoNarHash` gains a `narURL` parameter and, on `IsNotFound` from the join query, looks up the narinfo by `nar.URL{Hash, none, Query}.String()`. This is the minimal change to the verified-or-nothing policy: it still refuses to de-chunk a NAR with no narinfo at all.
- **fsck repairs by matching the narinfo URL to a nar_file by hash+query, any compression.** The narinfo URL may advertise a different compression than the NAR is stored under (CDC residue), so the lookup is compression-agnostic. Deletion is retained only when no nar_file matches — genuine orphans are still reclaimed.
- **Reconcile in `checkAndFixNarInfosForNar`, not at the raw `SetTotalChunks` call.** That function already runs on chunking completion (and post-store), already enumerates the narinfos matching the NAR URL, and already resolves the nar_file. Adding an idempotent `NarInfoNarFile` upsert there closes the race generically and also heals the non-CDC whole-file path for free, with a no-op cost in the common (already-linked) case.

## Risks / Trade-offs

- The reconcile upsert runs on every post-store/chunk-completion pass. It is a single `INSERT ... ON CONFLICT DO NOTHING` per matching narinfo — negligible, and a no-op when the link exists.
- The de-chunk URL fallback could, in principle, match the wrong narinfo if two narinfos shared a `nar/<H>.nar` URL; the subsequent content-verification (`sha256 == NarHash`) guards against an incorrect hash, so a wrong match fails safe (skip), never corrupts.

## Migration / Remediation

No schema change. Existing production rows stranded by the historical race are drained operationally: deploy this image, re-run `migrate-chunks-to-nar` (now resolves the NarHash via URL), then exit drain mode once the chunked count reaches zero. `fsck --repair` (now safe) backfills any remaining missing links.
