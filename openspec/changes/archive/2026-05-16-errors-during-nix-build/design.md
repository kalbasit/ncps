## Context

`ncps fsck` currently detects structural consistency issues between the database and storage: missing files, orphaned records, and for CDC NARs, chunk count and size mismatches. It does not verify the *content* of stored chunks — only their existence (`HasChunk` → `os.Stat`). This gap means a CDC NAR whose chunks are present and correctly counted can silently serve corrupt bytes to Nix clients, producing `error: bad archive: input doesn't look like a Nix archive` or `error: input compression not recognized`.

Chunks may have been stored with truncated or corrupt content by the pre-#1171/#1172 chunking bugs. The existing `narFilesWithSizeMismatch` check catches the case where the DB-recorded `file_size` differs from the narinfo `NarSize`, but not the case where `file_size` is correctly recorded while individual chunk bytes are wrong.

## Goals / Non-Goals

**Goals:**
- Add `--verify-content` flag to `ncps fsck` that reads and hashes each chunk's decompressed content and compares against the stored hash key.
- Add end-to-end NAR hash verification: concatenate chunk bytes in order and compare the SHA-256 digest against the narinfo `NarHash`.
- Report two new fsck categories — `narFilesWithCorruptChunks` and `narFilesWithHashMismatch` — with full repair support.
- Expose `fsck.verifyContent` in the Helm chart and pass `--verify-content` to the cron job when enabled.
- Document the flag, its I/O cost, and usage guidance in user-facing docs.

**Non-Goals:**
- Serving-time hash verification (hot path; out of scope).
- Verification of non-CDC NARs stored as whole files (Nix verifies these on download).
- Changes to chunk storage format or compression algorithm.
- Parallelising the verify-content phase (acceptable future optimisation).

## Decisions

### 1. Opt-in `--verify-content` flag (default: off)

Reading all chunk bytes is O(total NAR bytes) of I/O plus zstd decompression per chunk — potentially terabytes for a large cache. Making it opt-in prevents unexpected performance regressions for operators who run fsck on a schedule.

Alternatives considered: always-on (rejected — too expensive for routine runs); content sampling (rejected — a corrupt chunk that isn't sampled would go undetected).

### 2. Hash decompressed chunk content via streaming

Call `GetChunk(hash)` (which decompresses from zstd) and stream the result into `sha256.New()` via `io.Copy`. Do not buffer the full chunk with `io.ReadAll`. This keeps memory usage at O(read-buffer) rather than O(chunk-size).

`GetRawChunk` was considered, but chunk hashes are computed over the *raw* (pre-compression) content — consistent with how the chunker writes them — so decompressed content is the correct input.

### 3. Skip NAR hash check when a chunk fails content verification

If any chunk in a NAR fails, mark the NAR as `narFilesWithCorruptChunks` and skip the end-to-end hash check. Assembling from corrupt chunk bytes would produce a wrong hash regardless, and double-counting across categories would confuse the repair logic.

### 4. `--verified-since` still applies under `--verify-content`

Operators may combine `fsck --verify-content --verified-since 24h` to skip recently-verified NARs, making periodic full-content scans tractable on large caches.

### 5. Reuse existing repair cascade for new categories

Both new categories use the same repair path as `narFilesWithChunkIssues`: delete the `nar_file` record, cascade to orphaned narinfo, then remove orphaned chunks from DB and storage. No new repair logic is needed.

### 6. Chart value: `fsck.verifyContent`

camelCase, consistent with existing `fsck.verifiedSince` in `values.yaml`.

## Risks / Trade-offs

- **High I/O on large caches**: `--verify-content` reads every chunk byte in full. Mitigation: opt-in, documented cost, `--verified-since` to scope the scan.
- **False negatives if chunk hashes were recorded incorrectly at write time**: The chunker computes hashes from raw bytes before calling `PutChunk`; the write path is correct. Risk is negligible.
- **Lock contention during verify-content scan**: No change from existing behavior — fsck already holds per-NAR read locks; verify-content adds sequential I/O per NAR, not additional locking.

## Migration Plan

No database schema changes. No migration required. The `--verify-content` flag is purely additive.

Deploy order: update the binary, then optionally set `fsck.verifyContent: true` in the chart to enable a one-time content scan to find NARs corrupted by the pre-#1171 bug.

## Open Questions

None.
