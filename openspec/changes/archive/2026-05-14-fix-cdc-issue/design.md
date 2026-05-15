## Context

CDC chunking runs as a background goroutine in `pullNarIntoStore` (cache.go ~2479). The goroutine calls `storeNarWithCDCFromReader`, which:

1. Opens a CDC chunker, iterates over `chunksChan` accumulating `totalSize` and `chunkCount`.
2. On `!ok` (channel close = normal EOF from FastCDC), calls `UpdateNarFileTotalChunks(chunkCount, totalSize)`, marking the nar_file complete.

Two structural gaps make the bug possible:

**Gap A — no size validation at commit.** `storeNarWithCDCFromReader` receives `fileSize` (= `narInfo.NarSize`) as a parameter but never compares it against the accumulated `totalSize` before committing. If the decompression stream ends early and the decompressor returns a clean `io.EOF` (instead of `io.ErrUnexpectedEOF`), the chunker closes `chunksChan` normally, the select sees `!ok`, and `UpdateNarFileTotalChunks` is called with a truncated byte count. No error is returned.

**Gap B — fsck does not validate reassembled size.** `isNarFileChunkBroken` (fsck.go:1213) checks only: `len(chunks) != nf.TotalChunks` (count mismatch) and `HasChunk` for each chunk (physical existence). It does not compare `nf.FileSize` against the linked narinfo's `nar_size`. A truncated but internally-consistent CDC NAR (correct chunk count in DB, all chunk files present) passes fsck as healthy.

The root cause of the silent truncation is that the upstream HTTP/2 connection drops mid-stream (`GOAWAY` under concurrent load), the zstd reader returns an early `io.EOF` without surfacing `io.ErrUnexpectedEOF`, and ncps has no defense against this at the CDC commit site.

## Goals / Non-Goals

**Goals:**
- Prevent truncated NARs from being committed as complete CDC artifacts.
- Surface existing truncated CDC rows in `ncps fsck` so operators can find and repair them.
- Add a regression test that fails before the fix and passes after.

**Non-Goals:**
- Fixing the HTTP client to not retry on `GOAWAY` (separate concern, may improve but doesn't remove the need for commit-time validation).
- Detecting truncation at the client (HTTP response) layer — the client already fails with "NAR is incomplete"; we're fixing cache poisoning, not client-facing errors.
- Fixing other CDC bugs (stale lock, orphaned chunks) addressed in prior commits.

## Decisions

### Decision 1: Validate `totalSize == fileSize` at the CDC commit site

**Where**: In `storeNarWithCDCFromReader`, inside the `if !ok { ... }` branch (line ~1972), before calling `UpdateNarFileTotalChunks`.

**What**: After the chunker loop drains, compare `uint64(totalSize)` against `fileSize` (the `narInfo.NarSize` passed by the caller). If they differ:
- Log an error with both values and the narinfo hash.
- Return `fmt.Errorf("CDC chunking truncated: expected %d uncompressed bytes, got %d: %w", fileSize, totalSize, io.ErrUnexpectedEOF)`.
- Do NOT call `UpdateNarFileTotalChunks` — leave `total_chunks = 0` and `chunking_started_at` set (the existing stale lock recovery at 1-hour TTL will clean up partial chunks).

**Why this over patching the chunker**: The chunker operates on a plain `io.Reader` and correctly propagates non-EOF errors. The problem is that the zstd reader can return a clean `io.EOF` even on a truncated stream (observed behavior). Fixing this at the commit site is independent of decompressor error semantics and catches all truncation scenarios including future ones.

**Guard clause**: If `fileSize == 0` (narinfo malformed), skip the validation — don't fail on an unknown expected size.

**Alternative considered**: Wrap the decompressor to enforce `io.ErrUnexpectedEOF` when bytes read < expected — rejected because `fileSize` in the narinfo is the **uncompressed** NAR size, but the decompressor is operating on the **compressed** stream; detecting truncation requires knowing the uncompressed expected size, which is higher-level context that doesn't belong in a generic wrapper.

### Decision 2: Extend fsck to detect size-mismatched CDC NARs

**What**: Add a new DB query `GetCDCNarFilesWithSizeMismatch` (all three engines) that selects nar_files where `total_chunks > 0 AND file_size != narinfos.nar_size` via a join through `narinfo_nar_files`. Add a new field `narFilesWithSizeMismatch []database.NarFile` to `fsckResults`. Surface it in the summary table as "CDC NARs with size mismatch" and include it in `repairFsckIssues` using the same repair path as `narFilesWithChunkIssues` (delete nar_file + orphaned narinfos + orphaned chunks, allowing re-fetch on next request).

**Why not extend `isNarFileChunkBroken`**: `isNarFileChunkBroken` doesn't have access to `nar_size`; it only receives `database.NarFile` which has `FileSize` (the actual stored size) but not the narinfo's declared size. Adding a DB call per NAR file to fetch `nar_size` is possible but inefficient; a single join query is O(n) across all affected rows and more efficient for a batch fsck scan.

**Why `file_size` is the right field**: `nar_files.file_size` is set to `uint64(totalSize)` at CDC commit time, which is the actual bytes chunked. For a fully-chunked uncompressed NAR, `file_size == narinfos.nar_size`. Truncated rows will have `file_size < nar_size`. No need to recompute the sum from chunks.

### Decision 3: Regression test with a synthetic truncating reader

**Shape**: Create a `limitedReader` (or use `io.LimitReader`) that returns `io.EOF` after `N < narInfo.NarSize` bytes. Pass it through the CDC path via `storeNarWithCDCFromReader`. Assert that:
1. The function returns a non-nil error.
2. The nar_file in the DB has `total_chunks = 0` (not committed as complete).
3. No `verified_at` is set on the nar_file.

This test should be in `pkg/cache/` (integration style, using the existing test database helpers) to exercise the real DB path.

**Why not test via `pullNarIntoStore`**: That function runs CDC in a background goroutine and the error doesn't fail the HTTP handler. Testing at the `storeNarWithCDCFromReader` level is simpler and more direct for the validation logic.

## Risks / Trade-offs

- **Partial chunks left behind on truncation**: When validation fails, the nar_file stays with `total_chunks = 0, chunking_started_at = non-null`. The existing stale lock cleanup (after 1 hour) will purge them. Trade-off: chunks accumulate for up to 1 hour before cleanup. Acceptable — same behavior as the existing stale lock path. The alternative (delete chunks immediately on commit failure) adds complexity and could race with concurrent operations.

- **False positive if narinfo NarSize is wrong**: If an upstream serves a narinfo with an incorrect `NarSize`, a successful download would be rejected at the commit site. Mitigated by the `fileSize == 0` guard; if a wrong-but-nonzero `NarSize` is in the wild, this could cause repeated re-fetch loops. This is already a data correctness issue upstream; ncps failing loudly is better than silently caching wrong data.

- **New fsck query adds a JOIN across potentially large tables**: For large caches, the `GetCDCNarFilesWithSizeMismatch` query involves a 3-way join. This is a one-time scan during fsck, not in the hot path. Acceptable.

## Migration Plan

No schema changes. No data migration required.

- Fix ships in a single release.
- Operators can run `ncps fsck` to find and repair existing truncated CDC rows.
- `--repair` on the new "CDC NARs with size mismatch" category deletes the row + chunks, which causes re-fetch on next client request.

## Open Questions

- Should we also emit a Prometheus counter (`cdc_truncation_total`) when a size validation failure is detected? Useful for alerting in production. Deferred to a follow-up unless the team wants it in this fix.
- Should we emit a `warn`-level log from the CDC goroutine's error handler in `pullNarIntoStore` when `storeNarWithCDCFromReader` returns error? Currently the goroutine only logs at `debug`. Yes — this should be `error`-level to make the issue visible in logs (addresses the reporter's observation that "there are zero warn or error lines").
