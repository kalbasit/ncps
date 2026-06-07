## Context

Nix clients receive truncated NAR downloads from production ncps: responses that
begin as `HTTP 200` (with a `Content-Length`) then die mid-body
(`unexpected end of nar`, `truncated input`, `SSL_ERROR_SYSCALL`). See
`proposal.md` for the full motivation and production evidence.

Current serve path (`pkg/cache/cache.go`):

- `GetNar` → `getNarFromChunks` reads `total_chunks` in a transaction
  (`cache.go` ~7113), then **immediately** creates an `io.Pipe` and returns
  `(size, pipeReader, nil)` to the caller. The actual chunk-completeness check
  (`len(chunkHashes) != totalChunks` → `expected N chunks but got M`,
  `cache.go` ~7208) runs **inside the streaming goroutine**
  (`streamCompleteChunks`), i.e. after the reader is already handed back.
- `pkg/server/server.go` `getNar` then sets `Content-Length` (~938) and
  `WriteHeader(200)` (~966) and `io.Copy`s the pipe. When the goroutine hits the
  missing chunk it does `pw.CloseWithError(ErrNotFound)`, the copy aborts, the
  connection is reset, and the client has an un-fulfillable 200.

Root cause of the missing chunks: `nar_file_chunks.chunk_id → chunks(id)` is
`ON DELETE CASCADE`, so deleting a `chunks` row strips its junction links from
**every** referencing `nar_file` — including completed ones (`total_chunks > 0`) —
without resetting `total_chunks`. Nothing enforces `count(links) == total_chunks`.
Live DB: 71 of 2499 completed chunked NARs have `links < total_chunks`.

Key invariant we rely on: `total_chunks` is the **completion latch**. In
`storeNarWithCDC` (`cache.go` ~2142) `SetTotalChunks(N)` is written only after all
`recordChunkBatch` link writes have committed, in the same transaction that does
`ClearChunkingStartedAt()`. So `total_chunks > 0 && links < total_chunks` is never a
concurrent mid-chunking replica — it is always genuine post-completion loss.

## Goals / Non-Goals

**Goals:**

- A completed chunked NAR that cannot be reassembled is resolved to `HTTP 404`
  (synchronously, before any body is written) so Nix falls back / refetches —
  never a truncated `200`.
- The completeness check runs only on the `total_chunks > 0` fast path; the
  progressive (`total_chunks = 0`) path is untouched and HA-safe.
- `migrate-chunks-to-nar` / drain detects un-reassemblable NARs, skips them, and
  reports them rather than aborting; such NARs become purgeable so they self-heal.

**Non-Goals:**

- Changing the `ON DELETE CASCADE` FK or adding a DB-level
  `links == total_chunks` constraint via migration. The cascade is documented as
  root cause but the repair is operational (404 → purge → refetch), not schematic.
- Fixing the separate `total_chunks = 0` mid-chunking-orphan state (the domain of
  #1230 / PR #1317). Out of scope here.
- Pre-stating every physical chunk blob on the hot serve path (rejected below).

## Decisions

**Decision 1 — Validate junction-link completeness synchronously, before the pipe.**
In `getNarFromChunks`, on the `total_chunks > 0` branch, count `nar_file_chunks`
links for the `nar_file` (a cheap indexed `COUNT` on `nar_file_id`) inside the same
init transaction that already reads `total_chunks`. If `links != total_chunks`,
return `storage.ErrNotFound` immediately — before `io.Pipe`/the goroutine. The
handler already maps `storage.ErrNotFound → 404`, so no server-side change is
required for the happy mapping; we only move the existing `~7208` check earlier and
make it synchronous.
- *Alternative considered:* keep the check in the goroutine but have the handler
  buffer until first successful read before sending headers. Rejected: NARs are
  large; buffering defeats streaming and the count check is essentially free.

**Decision 2 — Scope the check to the completed path only.**
The progressive path (`total_chunks = 0`) must not gain this check. We gate strictly
on the existing `if totalChunks > 0` branch (`cache.go` ~7158). This preserves HA
correctness per the completion-latch invariant above.
- *Alternative considered:* validate on both paths. Rejected: would `404` NARs that
  another replica is actively chunking.

**Decision 3 — Physical-blob gaps stay best-effort on the serve path; caught on drain.**
Junction-link completeness is DB-cheap and covers the dominant case
(`expected N got M`). A junction link whose physical chunk blob is missing
(`error fetching chunk … not found`) is only knowable by touching storage; we do
**not** pre-stat all blobs on every serve (O(chunks) extra I/O on the hot path).
Instead, the drain/migrate path — which already reads every chunk to reconstruct —
is where blob gaps are authoritatively detected and reported (Decision 4). On the
serve path a blob-missing NAR still fails, but that is rarer and is the migration's
job to surface and purge.
- *Alternative considered:* pre-stat blobs on serve. Rejected on latency grounds.

**Decision 4 — Drain/migrate skips, continues, and reports.**
`MigrateChunksToNar` already returns `ErrMissingChunk` when reconstruction can't
find a chunk (`cache.go` ~7690). Change the *driver loop* (the command iterating
chunked NARs) to treat `ErrMissingChunk` (and the new link-count mismatch) as
*skip + record*, not *abort*. At the end, report counts and the list of skipped
hashes. Provide/confirm a purge path (`PurgeChunkedNar` exists at `cache.go` ~7800)
so skipped NARs can be removed and refetched.

## Risks / Trade-offs

- [A blob-missing (not link-missing) NAR still truncates on the serve path until
  drain purges it] → Mitigation: Decision 4 surfaces these during migrate; operator
  purges; subsequent request refetches. The 71 production NARs are link-missing, so
  Decision 1 covers them immediately.
- [Extra `COUNT` query per chunked-NAR serve] → Mitigation: it runs in the existing
  init transaction against the indexed `nar_file_chunks(nar_file_id)`; negligible
  versus streaming thousands of chunks.
- [Moving the check could regress the happy path] → Mitigation: tests assert a
  fully-linked NAR still streams `200` unchanged, and a mid-chunking (`tc=0`) NAR is
  never `404`'d.

## Migration Plan

No DB migration. Deploy the binary; the serve check takes effect immediately and the
71 broken NARs begin returning `404` → Nix refetches from upstream. Run
`migrate-chunks-to-nar` to drain the rest; its new report lists any
un-reassemblable NARs to purge. Rollback is a plain binary rollback (the only
behavioral change is 404-instead-of-truncated-200 plus a richer migrate report).

## Testing Notes

- **Serving:** the deterministic test seeds a completed `nar_file`
  (`total_chunks = N`) with fewer than `N` `nar_file_chunks` rows (mirroring the
  cascade loss) and asserts `GetNar` returns `storage.ErrNotFound` and that no body
  bytes are emitted before the error — exercising the public `GetNar` interface, not
  internals.
- **HA guard:** a second test seeds `total_chunks = 0` with `chunking_started_at`
  set and asserts the completeness check does not fire (progressive path).
- **Drain:** a test with a mix of reassemblable and link-missing NARs asserts the
  migrate loop skips the broken one, migrates the rest, and reports the skipped hash.

## Open Questions

- **RESOLVED — purge stays drain-only; the serve path is side-effect-free.** The
  serve-path completeness check returns `storage.ErrNotFound` (→ 404) and performs
  **no** write/purge. Purging an un-reassemblable NAR is done exclusively by the
  drain / `migrate-chunks-to-nar` path (which already maps `ErrMissingChunk` to
  `PurgeChunkedNar` and continues). Rationale: keep the read path free of write
  side effects and lock contention (Redis), avoid purge storms under concurrent
  requests for the same broken hash, and keep the 404 cheap. Production self-heals
  on the next drain run; the inline per-NAR purge logs (with `nar_hash`) plus the
  end-of-run counts provide the operator report. An async purge-on-404 was
  considered and rejected for the contention/storm risk.
