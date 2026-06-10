## 1. Reproduce (TDD red)

- [x] 1.1 Add `TestServeNoneRequestFromXzWholeFile` in `pkg/cache/narinfo_nar_compression_desync_serve_internal_test.go`: store a NAR whole-file as `.nar.xz`, no upstream, request it as `Compression: none`, assert it is served decompressed (was `not found`).
- [x] 1.2 Confirm the test fails on current code (RED).

## 2. Fix the serve path (TDD green)

- [x] 2.1 Add `wholeFileServeCompressions()` helper in `pkg/cache/cache.go` returning `[zstd, xz]` (preference order).
- [x] 2.2 Generalize `getNarFromStore`'s `Compression: none` branch to find whichever supported whole-file compression is stored (`storedComp`), read it, and decompress via `nar.DecompressReader(ctx, r, storedComp)`; keep transparent-zstd pass-through gated on `storedComp == zstd`; label the served stream `none`.
- [x] 2.3 Generalize `statNarInStore`'s `Compression: none` branch to report present when any servable whole-file compression is on disk.
- [x] 2.4 In `getNarFromStore`'s LRU touch, fall back to the stored compression's `nar_file` row when the served-representation (`none`) row is absent; only heal a missing record when neither row exists.
- [x] 2.5 Confirm `TestServeNoneRequestFromXzWholeFile` passes (GREEN).

## 3. Verify (no regressions)

- [x] 3.1 `TestCDCWholeFileServeReportsServedCompression` and all `TestGetNar*` still pass.
- [x] 3.2 `task fmt` clean.
- [x] 3.3 `task lint` clean (`wholeFileServeCompressions` is a function, not a global, to satisfy `gochecknoglobals`).
- [x] 3.4 `task test` (full unit suite) green.
