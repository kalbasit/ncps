## Context

`TestCDCBackends/.../Mixed_Mode` (`pkg/cache/cdc_test.go:284`) intermittently fails at line
316 with `error fetching the nar from the store: not found`. The test stores a whole-file
NAR with CDC disabled, enables CDC, stores a second (chunked) NAR, then retrieves both.

Root cause is a TOCTOU race in the read path (`pkg/cache/cache.go`):

1. `GetNar` computes `hasNarInStore = true` for the whole-file blob, then at
   `cache.go:1118-1123` calls `maybeBackgroundMigrateNarToChunks` — which spawns a
   **detached background goroutine** (`BackgroundMigrateNarToChunks`) that chunks the NAR
   and, once chunks are committed, **deletes the whole file** from `narStore`
   (`migrateNarToChunksCleanup`, `cache.go:8112`).
2. `GetNar` then *synchronously* calls `serveNarFromStorageViaPipe(narURL, hasInStore=true)`.
3. That function (`cache.go:3047-3076`) decides store-vs-chunks from `nar_file.total_chunks`.
   In the race window the DB still reports `total_chunks = 0`, so it picks the **store**
   branch and calls `getNarFromStore` → `narStore.GetNar` → `storage.ErrNotFound`
   (`cache.go:3152`), which is wrapped as the observed error.

Key ordering fact (verified at `cache.go:8040-8044`): the whole-file deletion happens
**after** the chunk data and `nar_file` records are committed. Therefore, whenever the whole
file is missing due to migration, the chunks are guaranteed to exist and be reassemblable.

This is a real production defect (spurious 404s on freshly-migrated NARs under concurrent
reads), not merely a test artifact; the test simply exposes it reliably under load.

## Goals / Non-Goals

**Goals:**
- Eliminate the spurious `not found` when a concurrent background migration deletes the
  whole file mid-serve, for the uncompressed serve path.
- Make `testCDCMixedMode` deterministic by exercising and asserting the corrected behavior.
- Preserve genuine `ErrNotFound` for NARs that truly have neither whole file nor chunks, and
  preserve `ErrNotFound` for compressed requests whose whole file is gone.

**Non-Goals:**
- Redesigning the background NAR→chunks migration or its scheduling/drain semantics.
- Changing lazy-chunking deletion behavior (delayed deletion is already race-free).
- Touching the compressed/`.nar.xz` serving contract.

## Decisions

### Decision 1 — Fall back to chunks on a whole-file store miss (uncompressed, CDC on)

In `serveNarFromStorageViaPipe`, when the store branch was chosen (`serveFromChunks == false`)
but `getNarFromStore` returns `storage.ErrNotFound`, **and** CDC is enabled **and** the
request is for the uncompressed NAR (`Compression == none`), retry via `getNarFromChunks`.
If the chunk read also misses, return the original `ErrNotFound`.

Rationale: the migration-ordering guarantee (chunks committed before whole-file delete)
makes the fallback safe and deterministic — if the whole file vanished, the chunks are
present. This closes the window regardless of how `total_chunks` is observed, with no locking
or synchronization added to the hot path.

- **Alternative A — order/serialize migration vs. serve (e.g. read-lock the whole file
  during delete):** rejected; adds contention to the serve path and still needs a fallback
  for cross-process races.
- **Alternative B — re-read `total_chunks` in a retry loop until it flips > 0:** rejected;
  busy-waiting with unclear bound; the direct fallback is simpler and bounded.
- **Alternative C — don't trigger background migration before serving:** rejected; the
  background migration is intentional and desirable; only its interaction with the serve is
  buggy.
- **Alternative D — fix the test only (await migration / disable it):** rejected; masks a
  real production 404 and contradicts the proposal.

### Decision 2 — Regression test drives the actual race window

Following TDD, first add a failing test that constructs the exact failing state — a NAR whose
chunks are committed (reassemblable) but whose whole file has been removed while
`total_chunks` is observed as `0` on the store branch — and asserts `GetNar` returns the full
original bytes via the chunk fallback. The hardened `testCDCMixedMode` then asserts mixed-mode
retrieval succeeds regardless of background-migration timing.

## Risks / Trade-offs

- **[Fallback masks a genuinely corrupt/missing NAR]** → The fallback only engages on
  `ErrNotFound` from the store and still returns `ErrNotFound` when chunks are also absent or
  not reassemblable (the existing `chunked-nar-serving-integrity` completeness check governs
  reassembly), so no truncated 200 is introduced.
- **[Extra store-read miss in the race]** → One additional metadata/store lookup only in the
  rare race; steady-state path is unchanged.
- **[Compressed-request regression]** → Guarded: fallback is gated on `Compression == none`;
  compressed requests keep returning `ErrNotFound` so clients fall back upstream.

## Migration Plan

Pure code change; no schema or data migration. Deploy normally. Rollback is a straight
revert — behavior reverts to the pre-fix race (no data-format change to undo).

## Open Questions

- None blocking. The exact placement of the fallback (`serveNarFromStorageViaPipe` vs. a thin
  wrapper around `getNarFromStore`) is an implementation detail to settle during TDD; the
  spec requirement is layer-agnostic.
