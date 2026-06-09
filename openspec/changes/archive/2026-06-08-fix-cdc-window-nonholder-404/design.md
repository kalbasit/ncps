## Context

Reproduced by `task test:inflight-staging-contention --window chunking`: with CDC enabled, a lock-losing replica returns HTTP 404 for `/nar/<hash>.nar.xz` while the lock holder serves 200. Root cause traced through `Cache.GetNar` and `pollForDownloadOrTakeOver` (`pkg/cache/cache.go`):

- The lock-loser polls in `pollForDownloadOrTakeOver`. Each tick checks, in order: (1) `hasAsset(coordCtx)` → return a "served_by_peer" completed `ds` (line 6546); (2) `TryLock` takeover (6577); (3) `stagingActive && stagingServeReady` → return `ds.stagingServe` (6594).
- `hasAsset` is `isServable` (`prePullNar:~4319`). With CDC enabled and the holder **actively chunking**, `isServable` returns **true** (nar_file row exists, `ChunkingStartedAt != nil`, `cdcChunkerLive()` true) even though `total_chunks == 0`. So tick step (1) fires first and returns a completed `ds` — intentional, to route peers into progressive chunk streaming (`GetNar:1314-1316`).
- Back in `GetNar`, the completed `ds` (`closed=true`, no `stagingServe`) takes the `!canStream` branch (1317) → `serveNarFromStorageViaPipe` → because the NAR is chunks-only, `serveFromChunks` is true and the requested compression is `xz`, so it returns `storage.ErrNotFound` at **`cache.go:3465`** ("NAR is only available as chunks, cannot serve as xz") → 404.
- With CDC **disabled**, `isServable` is false (no chunk store), so step (1) is skipped, step (3) reaches staging, and `serveNarFromStaging` serves the parts **transcoded to xz** → 200. That transcoding path is exactly what the CDC case never reaches.

So: in-flight staging (which transcodes to the requested compression) is short-circuited under CDC by the `hasAsset` early-return, and the fallback chunk-serve path cannot satisfy a compressed request.

## Goals / Non-Goals

**Goals:**
- A contended CDC lock-loser serves a complete, byte-identical NAR (in the requested compression) instead of 404 — matching the CDC-off path.
- Preserve the existing invariants: progressive chunk streaming for the no-staging CDC case, and takeover-before-staging (D5, so a dead holder's truncated staging prefix is never served).
- Lock the behavior with the chunking-window e2e (local + s3) plus focused `pkg/cache` unit tests.

**Non-Goals:**
- Reworking the CDC chunking algorithm, part-object format, or narinfo URL normalization.
- The download-window staging fix (already shipped).
- Changing steady-state (post-chunking) serving.

## Decisions

**D1 — The fix is NOT a simple reorder: the tick conditions have a cyclic priority that must be resolved by splitting the served-by-peer predicate by state.**
A naive reorder is unsatisfiable. During active chunking the three conditions demand contradictory orders:
- staging must precede `hasAsset` (else an actively-chunking NAR hits served-by-peer → chunk-serve → 404 on `xz`);
- `hasAsset` must precede takeover (else a *finished* peer that released its lock is re-downloaded instead of served);
- takeover must precede staging (invariant D5: an acquirable lock ⇒ dead holder ⇒ truncated staging prefix).

That is `staging < hasAsset < takeover < staging` — a cycle. The root reason is that `hasAsset` (`isServable`) **conflates two distinct states**: "peer finished, asset fully materialized" and "peer actively chunking (`total_chunks==0`, chunker live)". The served-by-peer early-return must fire only for the *finished* state; the *actively-chunking* state must defer to: staging (when active + parts ready) or progressive chunk streaming (no-staging). Concretely, the coordination decision must distinguish four states per tick:
1. **dead holder** (lock acquirable) → takeover + restart (D5, stays first);
2. **finished** (`HasNarInStore` OR `total_chunks>0`) → served-by-peer completed `ds`;
3. **actively chunking + staging active + parts ready** → `ds.stagingServe` (transcodes to the requested compression);
4. **actively chunking + no staging** → completed `ds` routed to progressive chunk streaming (current behavior; preserves `GetNar:1314-1316`).

This is a restructuring of the coordination decision, not a line swap. *Alternative considered:* globally narrowing `isServable` — rejected: `isServable` is used elsewhere as "can serve now" (including active chunking for progressive streaming); only the served-by-peer decision inside `pollForDownloadOrTakeOver` should split finished vs. actively-chunking.

**D2 — Leave the no-staging CDC path on progressive chunk streaming.**
When staging is disabled or no parts are ready, behavior is unchanged: `hasAsset` returns the completed `ds` and `GetNar` routes to `getNarFromChunks`. The compressed-request-from-chunks 404 in that no-staging path is a *separate, pre-existing* limitation (a chunk-serving replica cannot synthesize `xz`); it is out of scope here and only reachable without the staging feature.

**D3 — Verify staging actually produces parts in CDC mode.**
The e2e showed no producer error and no staging serve, consistent with the producer never being reached (hasAsset short-circuit) rather than a producer failure. Part of implementation is confirming, with the reorder, that the CDC holder's staging producer tails a valid temp file and the loser serves transcoded bytes end-to-end.

## Risks / Trade-offs

- **[Reordering the coordination tick regresses progressive streaming or takeover]** → Keep takeover strictly first (D5 preserved); only move staging ahead of `hasAsset`. Gate behind `stagingActive`, so feature-off behavior is byte-for-byte unchanged. Validate the full `pkg/cache` suite (coordination, cdc_*, takeover, recovery_gc) plus the chunking-window e2e.
- **[Serving from staging when the peer actually just finished]** → Harmless: staging parts are complete bytes; after retention they are reclaimed and `hasAsset` resumes. A brief preference for staging over direct storage is acceptable.
- **[Staging may not engage if the CDC holder's temp file lifecycle differs]** → Confirm during implementation (D3); if the CDC producer can't stage, escalate as a separate finding rather than forcing a partial fix.

## Migration Plan

Pure `pkg/cache` behavior change; no schema/config/API. Ships as a normal deploy; rollback is a git revert. The chunking-window e2e is the multi-process acceptance gate.

## Open Questions

- Does moving `TryLock` ahead of `hasAsset` in the tick change the takeover cadence meaningfully? (Both run once per 200ms tick; ordering only affects which wins when several conditions are simultaneously true.)
- Should the no-staging chunk-serve compressed-request 404 (D2) be filed as its own follow-up, or is staging-on the intended production configuration for multi-replica CDC?
