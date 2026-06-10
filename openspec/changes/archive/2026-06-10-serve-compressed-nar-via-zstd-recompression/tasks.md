## 1. Reproduce (TDD red)

- [x] 1.1 Add a test in `pkg/cache` that enables CDC, stores+chunks a NAR (uncompressed chunks, no whole file), requests it as `Compression: zstd`, and asserts the served stream decompresses (zstd) to the original NAR. Confirm it fails today with a 404 / "cannot serve as zstd" (RED).

## 2. Implement zstd recompression on serve (TDD green)

- [x] 2.1 In `serveNarFromStorageViaPipe` (`pkg/cache/cache.go`), replace the unconditional `serveFromChunks && compression != none` 404 with: for `Compression: zstd`, reassemble the uncompressed bytes via `getNarFromChunks` (none URL) and recompress to zstd through an `io.Pipe` + `zstd.NewPooledWriter` (mirroring the existing pipe/SafeGo pattern); return the reader labeled `zstd`, size `-1`. Keep the 404 for all other non-`none` compressions.
- [x] 2.2 Confirm the new test passes (GREEN) and the served compression is reported as `zstd`.

## 3. Verify (no regressions)

- [x] 3.1 `none`-from-chunks, zstd-from-stored-`.nar.zst`, and the xz-not-producible fallback paths still behave as before (existing `TestGetNar*` / CDC serve tests pass).
- [x] 3.2 `task fmt`, `task lint`, `task test` all green.
