## Why

Nix clients pulling from `ncps.nasreddine.com` get **truncated NAR downloads** —
`error: bad archive: unexpected end of nar encountered`,
`failed to read compressed data (truncated input)`, and
`SSL_read: SSL_ERROR_SYSCALL` on responses that started as `HTTP 200`. The
narinfo-500 fix (`fix-purged-narinfo-500`) is confirmed gone; this is a distinct,
NAR-side failure.

**Root cause (confirmed against the live production DB):** a subset of *completed*
chunked NARs cannot be reassembled, yet the cache promises and starts a `200`
response anyway:

- The serve fast path `streamCompleteChunks` (`pkg/cache/cache.go` ~7208) enters
  on `total_chunks > 0`, then checks `len(chunkHashes) != totalChunks` **inside
  the streaming goroutine** — i.e. *after* `pkg/server/server.go` has already
  written `200 OK` + `Content-Length` and begun `io.Copy`. A completeness failure
  (`expected N chunks but got M: not found`) therefore surfaces as a **truncated
  200**, and Nix cannot fall back to its next substituter (it already committed to
  the 200).
- These NARs lost junction rows because `nar_file_chunks.chunk_id → chunks(id)` is
  `ON DELETE CASCADE`: deleting a `chunks` row cascade-wipes its junction links
  across **all** nar_files — including completed ones — **without** resetting
  `total_chunks`. No invariant prevents `count(links) < total_chunks`.

**Production evidence** (DB `pg17-vchord-cluster`): of 2499 completed chunked
NARs, **71** have `links < total_chunks` (the truncating set; loss is large and
scattered, e.g. 7290 of 7646 links gone). Separately, 22 NARs are in the
`total_chunks = 0` mid-chunking-orphan state.

**Relationship to #1230 / PR #1317:** *not the same bug.* #1230/#1317 address the
22 `total_chunks = 0` mid-chunking crash orphans (and #1317 is unmerged and gated
on CDC being enabled, so it would not even run now that CDC is disabled in drain
mode). The 71 truncating NARs are completed (`total_chunks > 0`) rows that lost
links via the cascade — a state #1317 does not touch. This change targets that
state.

**HA correctness:** `total_chunks` is the completion latch — it is set only at the
end of chunking, after all links are durably committed, in the same transaction
that clears `chunking_started_at` (`cache.go` ~2142). So `total_chunks > 0 &&
links < total_chunks` is **never** a concurrent mid-chunking replica; it is always
genuine post-completion loss. The synchronous completeness check is therefore safe
on the `total_chunks > 0` fast path and must NOT be applied to the progressive
(`total_chunks = 0`) path, which legitimately waits for chunks.

## What Changes

- **Serving (defense, fast path only):** before committing the response, validate
  that a completed chunked NAR (`total_chunks > 0`) actually has all its junction
  links. On mismatch, `getNarFromChunks` returns `storage.ErrNotFound`
  **synchronously** (before the pipe/`200`/`Content-Length`), so the handler
  responds `HTTP 404` and Nix falls back / refetches upstream — never a truncated
  `200`. The progressive (`total_chunks = 0`) path is left unchanged.
- **Drain hardening:** `migrate-chunks-to-nar` / drain mode must detect a
  chunked NAR that cannot be reassembled (missing junction links, or a referenced
  chunk whose blob is absent) and **skip it with a clear report** instead of
  attempting reassembly and failing opaquely. Surface a count of un-reassemblable
  NARs so the operator can act, and make such a NAR purgeable so the next request
  refetches it cleanly from upstream.
- Regression tests: a completed chunked NAR with a missing link is served as
  `404` (not a truncated `200`); the drain path reports and skips it rather than
  erroring mid-stream.

## Capabilities

### New Capabilities
- `chunked-nar-serving-integrity`: On the completed-chunk (`total_chunks > 0`)
  serve path, the cache MUST verify reassembly is possible before committing the
  HTTP response, and MUST resolve an un-reassemblable chunked NAR to `404`
  (upstream fallback) rather than a truncated `200`.

### Modified Capabilities
- `cdc-drain-mode`: drain / `migrate-chunks-to-nar` MUST detect un-reassemblable
  chunked NARs (missing links or absent chunk blobs), skip them without aborting
  the run, and report them so they can be purged and refetched — rather than only
  failing at serve time.

## Impact

- **Code:** `pkg/cache/cache.go` (`getNarFromChunks` / `streamCompleteChunks`
  pre-stream completeness check; drain/migrate detection + reporting),
  `pkg/server/server.go` (already maps `ErrNotFound → 404`; verify no truncated
  `200`). Possibly `cmd/` reporting for the drain command.
- **Behavior:** un-reassemblable chunked NARs become `404` (self-healing via
  upstream refetch) instead of corrupt downloads. No schema change required for
  the serving fix; the cascade FK and the absence of a `links == total_chunks`
  invariant are documented as the root cause but repaired operationally (purge +
  refetch) rather than by a migration.
- **Operational:** the 71 currently-broken NARs in production heal on next request
  once they `404` (Nix refetches from upstream); the drain report makes them
  visible.
