## Context

`pkg/cache/cache.go` `getNarFromStore()` is the whole-file NAR serve path (chunked NARs go through `getNarFromChunks`; routing in `serveNarFromStorageViaPipe` sends a request to chunks only when `nr.TotalChunks > 0 && narURL.Compression == none`). For an `xz` request it therefore stays on the whole-file path, opens the xz file (`storeURL.Compression = xz`, `decompress = false`), and streams the raw xz bytes.

The defect is `cache.go:3441`:

```go
// Update narURL.Compression to match the record found in DB.
narURL.Compression = nar.CompressionType(nr.Compression)
```

`nr` comes from `getNarFileFromDB()`, which is **CDC-first**: it prefers the chunked `compression=none` row when one exists. During the lazy-chunking transition a NAR has both an xz whole file (kept until delete-delay) and a chunked `none` row — so this overwrites the response compression to `none` while the body is raw xz. The HTTP layer then sends raw xz bytes with `Content-Length` = xz size (< `NarSize`), and `nix` reports `NAR ... is incomplete` / `input compression not recognized`.

## Goals / Non-Goals

**Goals:**
- The whole-file serve path reports the compression of the bytes it actually serves.
- A `pkg/cache` regression test reproduces the bug (TDD red) and proves the fix (green).
- Fix the deterministic "enable CDC on an existing whole-file cache" failure.

**Non-Goals:**
- No change to chunked reassembly (`chunked-nar-serving-integrity`), the CDC-first `getNarFileFromDB` selection logic, or lazy-recovery scheduling.
- No DB schema change / migration.

## Decisions

**1. Make the whole-file serve path authoritative about compression (primary fix).**
The bytes streamed by `getNarFromStore` are determined by which file it opened (`storeURL`) and whether it transparently decompressed zstd→none — never by `getNarFileFromDB`. So the returned compression MUST be derived from the actual serve, i.e. the original requested `narURL.Compression` (which, after the `none`→`.nar.zst` transparent-decompress branch, already equals the bytes' compression). Remove the `narURL.Compression = nr.Compression` overwrite at `cache.go:3441`.

- Alternative considered: change `getNarFileFromDB` to look up by the *served* compression. Rejected — that function's CDC-first selection is correct for record-touching/healing; only the serve path's compression *reporting* is wrong. Narrower to fix the reporting.
- The DB-record touch (`last_accessed_at`) below the overwrite uses `narURL.Compression` in its WHERE clause; after the fix it touches the row matching the served (xz) representation, which is the correct row to mark accessed. Verify the touch still targets an existing row (the xz row exists during the transition window).

**2. TDD regression test in `pkg/cache`.**
Use the `cacheFactory`/`newContext()` harness and xz `testdata` (`Nar1` has `Compression: xz`). Enable CDC + lazy with a long delete-delay so the xz whole file is NOT deleted, `MigrateNarToChunks` to create the coexisting chunked `none` row, then `GetNar` the xz URL and assert `nu.Compression == xz` and the streamed bytes match the xz file (decompress to `NarSize`). This fails today (returns `none`) and passes after the fix. Place in a new `pkg/cache/cdc_whole_file_serve_internal_test.go` if unexported access is needed; otherwise extend `cdc_test.go`.

**3. Secondary hardening only if proven necessary.**
There is a secondary TOCTOU where lazy cleanup could delete the xz bytes mid-`io.Copy`. Only add a read-lock/completeness guard if a test demonstrates it; otherwise keep the change minimal (the primary fix resolves the deterministic failure).

## Risks / Trade-offs

- [Touch WHERE-clause targets a now-different row] → After the fix `narURL.Compression` stays as served; confirm the `last_accessed_at` update still matches an existing nar_file row (xz row present during transition). Mitigation: assert in the test that GetNar succeeds end-to-end (touch errors would surface).
- [Other callers depend on the old (wrong) relabel] → Audit callers of `getNarFromStore`/`GetNar` that read the returned `nar.URL.Compression`; the HTTP server uses it for `Content-Encoding`/`Content-Length`, which is exactly what we are correcting. Low risk.
- [openspec-guard blocks merge] → archive this change before merge.

## Migration Plan

Pure code fix; no data/schema migration, no rollback steps. Deploy normally. Forward-compatible: corrects responses for pre-existing whole-file NARs after CDC enablement.

## Open Questions

- Is the secondary mid-serve deletion TOCTOU reachable under the fixed path, or does the primary fix plus existing completeness guards already cover it? Resolve via the regression test; only harden if reproducible.
