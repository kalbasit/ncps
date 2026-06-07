## Context

After the self-completing-cdc-drain change, a `migrate-chunks-to-nar` pass drains everything — but only when an operator runs it. fsck runs routinely (often daily) and should keep residue from lingering. The hazard: fsck must never purge a *legitimately* chunked NAR (normal during active CDC). A chunked NAR can also be transiently un-de-chunkable (mid-chunking; narinfo written slightly after the nar_file). So immediate purge-on-sight is unsafe.

## Goals / Non-Goals

- **Goal**: fsck keeps chunked-NAR residue from accumulating, safely, as a routine janitor — normalizing what it can and reclaiming the truly-dead only after a grace window.
- **Non-Goal**: replacing the operator drain. fsck is the steady-state net; `migrate-chunks-to-nar` remains the bulk drain.

## Decisions

- **Persistent flag, not in-memory.** A nullable `nar_file.dechunk_residue_flagged_at` timestamp survives across fsck runs (and process restarts), which is what makes "purge only if still suspect a day later" possible. Reuse the existing de-chunk resolver (`linkedNarinfoNarHash`) to decide recoverability, so fsck and the drain agree on what is verifiable.
- **Two tiers, evaluated per chunked nar_file:**
  1. *Recoverable* (a narinfo with a resolvable NarHash exists, but inconsistent URL): relink + normalize URL to none immediately; clear any flag. Safe in any CDC state — touches no chunks.
  2. *Un-de-chunkable* (no resolvable NarHash): if unflagged, set the flag and stop. If flagged and `now - flagged_at >= grace` and still un-de-chunkable, purge. If it became recoverable, clear the flag.
- **Grace window is configurable**, default ~24h, so two daily fsck runs reclaim. A NAR mid-chunking (or with a not-yet-written narinfo) resolves long before 24h, so it is never purged.
- **Guard against in-flight chunking.** Skip flagging/purging a row whose `chunking_started_at` is recent (within the chunking lock TTL) — it is actively being written, not residue.
- **Reuse `PurgeChunkedNar`** for the actual reclamation (same self-heal path the drain uses): the narinfo remains and re-pulls from upstream on next access.

## Risks / Trade-offs

- A genuinely-dead chunked NAR survives one extra fsck cycle (the grace window) before reclamation. Acceptable — it is served from chunks meanwhile, and the safety of never purging a transient/legitimate chunked NAR is worth the delay.
- The flag adds one nullable column; the migration is additive and forward-only.

## Validation

TDD: recoverable→normalized; first-detection→flagged-not-purged; aged-and-still-suspect→purged; became-recoverable→unflagged. Then deployed and run against the **real production residue** (after a de-chunk pass leaves any un-de-chunkable rows), confirming fsck flags then reclaims them across two runs and that de-chunk + fsck together leave a clean, consistent state. Exercised end-to-end by the CDC-lifecycle e2e tests.
