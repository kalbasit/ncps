## Why

Issue [#1155](https://github.com/kalbasit/ncps/issues/1155): when CDC is enabled and an upstream HTTP/2 stream drops early under concurrent load (e.g., Cloudflare-fronted `cachix.org` returning `GOAWAY`), ncps silently commits a truncated NAR as a "complete" CDC-chunked artifact. The chunker treats an early `io.EOF` as a normal end-of-stream, sets `total_chunks` and `verified_at`, and fsck subsequently certifies the truncated row as healthy. Clients then fail with `NAR for '...' is incomplete`, and the cache poisoning is invisible (no warning, no error in logs).

The reproduction shows real cache corruption today: 6 CUDA libraries from `nix-community.cachix.org` consistently truncate to a few hundred KB or low single-digit MB against a declared `nar_size` of 100s of MB. Truncation is durable until manual DB cleanup.

## What Changes

- Validate total bytes consumed by the CDC chunker against the narinfo's declared `NarSize` at commit time. On mismatch, abort the transaction, do not set `total_chunks`, and surface a typed error so the request fails loudly instead of silently committing a partial result.
- Treat any non-`nil` non-`io.EOF` reader error during CDC ingestion as fatal for the commit. Unexpected `io.ErrUnexpectedEOF` from upstream must not be swallowed by the chunker's success path.
- Extend fsck to recompute the sum of chunk sizes for each CDC-stored NAR and compare against the narinfo's `nar_size`. Flag size mismatches as corruption (not "healthy"), and provide a path to remediate (purge + re-fetch).
- Add a regression test reproducing the race: a `pullNar`-equivalent flow where the upstream reader returns `n bytes + io.ErrUnexpectedEOF` (or premature `io.EOF`) mid-stream. The test must fail before the fix and pass after.

## Capabilities

### New Capabilities

(none — this is a correctness fix to existing CDC behavior)

### Modified Capabilities

- `cdc-chunking`: NAR ingestion via CDC MUST validate the total uncompressed byte count equals the narinfo's declared `NarSize` before committing `total_chunks`. Premature stream termination MUST result in a failed commit, not a partial-but-marked-complete row.
- `fsck`: For CDC-stored NARs, fsck MUST verify that the sum of `chunks.size` for the chunk set equals `narinfos.nar_size`. Size-mismatched rows MUST be reported as corrupt.

## Impact

- **Code**: `pkg/cache/cache.go` (CDC ingestion: `storeNarWithCDC` / `storeNarWithCDCFromReader`, around the `pullNarIntoStore` → chunker boundary). `pkg/cache/fsck.go` or equivalent fsck verification path.
- **Database**: No schema change. The fix is enforced in application logic before `UpdateNarFileTotalChunks` is called.
- **Tests**: New regression test in `pkg/cache/` simulating early stream termination during CDC ingestion. New fsck test asserting size-sum validation for CDC NARs.
- **Behavior change**: Requests that previously silently committed truncated NARs will now return an error and not pollute the cache. This is the intended correctness improvement; no migration is required for existing truncated rows (operators can run the new fsck to identify and purge them).
- **No I/O or memory regressions expected**: the byte counter is already implicit in the chunker pipeline; the validation is an O(1) comparison at commit time.
