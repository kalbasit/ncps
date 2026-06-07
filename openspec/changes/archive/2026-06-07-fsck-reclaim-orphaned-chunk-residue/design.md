## Context

`ncps fsck` gates all chunk-related work behind a `cdcMode` boolean computed at `pkg/ncps/fsck.go` (the "5. Detect CDC mode" block). Today `cdcMode` is true iff:

1. DB config key `cdc_enabled == "true"`, or
2. (fallback) any `nar_file` has `total_chunks > 0`.

Only when `cdcMode` is true does fsck call `getChunkStorageBackend(...)` and pass a non-nil `chunkStore` into `collectFsckSuspects(...)`, which is what surfaces `orphanedChunksInDB` / `orphanedChunksInStorage` and lets `--repair` reclaim them.

A CDC→whole migration ends by (a) `serve` restart hitting `initCDCDrainMode` with `chunkedCount == 0`, which calls `DeleteCDCConfig()` (removes `cdc_enabled/min/avg/max`), and (b) every NAR being de-chunked so `total_chunks = 0` everywhere. Both `cdcMode` signals are now false — but the `chunks` table and chunk storage may still hold orphaned data (orphaned chunks are referenced by no `nar_file`, so signal 2 structurally cannot see them). A later `fsck` therefore initializes no chunk store and silently skips reclamation. Observed live 2026-06-07: ~5.5M orphaned chunk rows were reclaimable only because the single `fsck` run started ~46s before drain cleared the config; the sole recovery otherwise is manually re-inserting `cdc_enabled=true`.

`entchunk` is already imported in `fsck.go`. The chunk store backend is derived from storage flags (local `/store` path or S3), not from CDC config keys — so initializing it post-drain is already safe (`initCDCDrainMode` does the same).

## Goals / Non-Goals

**Goals:**
- Make a post-drain `fsck` detect and (under `--repair`) reclaim orphaned chunk residue without operator intervention.
- Keep the detection cheap (one indexed existence query) and the behavior obvious in logs.
- Preserve all existing gating: dry-run reports only; `--repair` mutates.

**Non-Goals:**
- No new CLI flags or config keys.
- Do not re-enable CDC writes or recreate the deleted `cdc_enabled` config.
- Do not change chunk creation, serving, migration, or the reclaim/cascade logic itself — only *when* `cdcMode` is reached.

## Decisions

**D1: Add a third `cdcMode` signal — non-empty `chunks` table.**
Append a clause to the detection block: if signals 1 and 2 are false, run `dbClient.Ent().Chunk.Query().Exist(ctx)`; if true, set `cdcMode = true`. Rationale: orphaned chunks always leave `chunks` rows behind (DB rows are deleted last during reclaim), so a non-empty table is the reliable, dialect-agnostic proxy for "residue may exist." `Exist` compiles to `SELECT EXISTS(SELECT 1 FROM chunks ...)`, indexed and O(1)-ish — no full scan.
- *Alternative considered — walk chunk storage* to catch stray chunk *files* with no DB row (`orphanedChunksInStorage`): rejected as the primary signal because it costs a full storage walk on every `fsck` (including non-CDC caches) just to decide a boolean. The existing `orphanedChunksInStorage` walk still runs *after* `cdcMode` is enabled, so once the DB signal trips, stray files are still reclaimed. The only uncovered corner — chunk files present with a totally empty `chunks` table — is implausible (files are deleted before/with their rows) and out of scope.
- *Alternative considered — check storage `/store/chunks` dir non-empty*: same walk cost, backend-specific; rejected.

**D2: Emit a distinct residue-detection log.**
When `cdcMode` is enabled solely by signal 3, log an `Info` like `"CDC mode enabled: chunk residue detected (orphaned chunks present after CDC disabled); reclaiming under --repair"`. It MUST be distinguishable from the existing signal-2 fallback `Warn` (`"cdc_enabled not set ... but chunked nar_files exist ..."`). Rationale: a post-drain operator seeing fsck do chunk work should immediately understand why, without it looking like CDC silently came back.

**D3: Precedence preserved.** Signals evaluated 1→2→3; first match wins and short-circuits, so active CDC (signal 1) never emits the residue log. Keeps existing behavior byte-for-byte when CDC is active or NARs are still chunked.

**D4: TDD.** Add failing unit/integration tests first (residue table-state permutations) against the detection function, then implement. Existing fsck tests under `pkg/ncps/fsck*_test.go` and `pkg/ncps/fsck_chunked_residue*_test.go` are the harness.

## Risks / Trade-offs

- **[A fresh, mid-CDC cache momentarily has chunk rows but no config]** → Not a regression: that state already trips signal 2 (chunked nar_files exist) or signal 1; signal 3 only adds coverage, never removes it.
- **[Extra query on every `fsck` run]** → One indexed `EXISTS` only when signals 1 and 2 are both false (i.e., the non-CDC / post-drain path). On active-CDC runs it is never reached. Negligible.
- **[Stray chunk files with an empty `chunks` table go unreclaimed]** → Accepted/out of scope (see D1); implausible given deletion ordering. Could be a future `--reclaim-chunk-storage` follow-up if ever observed.
- **[Operator confusion: "is CDC back on?"]** → Mitigated by D2's explicit, distinct log wording.

## Migration Plan

Pure code change in `pkg/ncps/fsck.go`; no schema migration, no config change, no new flags. Ship normally. Rollback = revert the commit. Forward-compatible: an old `fsck` binary simply retains today's behavior (won't detect post-drain residue), so deploying does not strand anything that wasn't already stranded.

## Open Questions

- Log wording for D2 — exact phrasing to be finalized in implementation (must not imply CDC writes re-enabled).
