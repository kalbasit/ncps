## Context

The `chunks-to-nar-migration` capability is implemented across two layers:

1. **Single-NAR operation** — `Cache.MigrateChunksToNar(narURL, forceReclaim)` (`pkg/cache/cache.go`). It is verified-or-nothing: it resolves the expected NarHash from a referencing narinfo (join link, else hash-aware URL match), reconstructs the NAR, content-verifies it, and only then flips the record to whole-file. If it cannot resolve a NarHash it returns `ErrNoNarHashToVerify` and **leaves the NAR chunked** (it never deletes/truncates what it cannot verify). Other failure sentinels: `ErrMissingChunk`, `ErrNarHashMismatch`, `ErrNarAlreadyWholeFile`, `ErrMigrationInProgress`.

2. **Batch pass** — `pkg/ncps/migrate_chunks_to_nar.go` iterates all chunked `nar_file` rows and calls the operation. Its error switch (lines 419-462) treats `ErrMigrationInProgress` / `ErrNarAlreadyWholeFile` as skips, context-cancellation as a non-purging failure, and **any other error** (including `ErrNoNarHashToVerify`, `ErrMissingChunk`, `ErrNarHashMismatch`) as a signal to `PurgeChunkedNar` — so the pass drives the chunked count to zero.

The spec documents both layers but does not label which requirement governs which, so "skip … SHALL NOT delete" and "purge, not skipped" read as a contradiction.

## Goals / Non-Goals

**Goals:**
- Make the spec internally consistent by attributing "skip" to the single operation and "purge / drive-to-zero" to the pass.
- Keep the spec faithful to the implemented behavior (verified at the cited lines).

**Non-Goals:**
- Any behavior change. The code is already consistent; only the wording is reconciled.
- Re-litigating the verified-or-nothing or purge policies themselves.

## Decisions

**1. Reconcile by layering, not by picking skip XOR purge.**
Both behaviors are correct and intended; they are not alternatives. The operation skips (refuses to de-chunk/delete an unverifiable NAR); the pass purges what the operation skipped. The spec will state this explicitly.
*Alternative considered*: change the code so the operation itself purges (eliminating the layer distinction). Rejected — it would make `MigrateChunksToNar` destructive on a bare verification miss, removing the safety seam (e.g. a transient missing-narinfo race would delete a possibly-healthy NAR) and conflating "this run can't verify it" with "this NAR is permanently bad." The current two-layer design is deliberate and safer.

**2. Edit the two requirements that read as conflicting.**
- "De-chunk MUST resolve the verification NarHash via the narinfo URL when no join link exists" — its skip language and the "…is skipped" scenario are scoped to the single `MigrateChunksToNar` operation, and the scenario notes the batch pass subsequently purges (cross-referencing the drive-to-zero requirement).
- "The de-chunk pass MUST always drive the chunked count to zero" — reaffirmed as the pass-level guarantee that purges whatever the operation skipped.

## Risks / Trade-offs

- [Wording implies a behavior the code does not have] → mitigated by citing exact files/lines during apply/verify; if a mismatch is found, it is surfaced, not papered over.
- [Future readers re-introduce the contradiction] → the explicit "operation vs pass" framing in both requirements makes the layering self-documenting.

## Migration Plan

Spec-only; no migration, no deploy concern. Rollback is reverting the spec edit.

## Open Questions

None — the authoritative policy was determined from the code (`pkg/cache/cache.go:8364-8377`, `pkg/ncps/migrate_chunks_to_nar.go:419-462`).
