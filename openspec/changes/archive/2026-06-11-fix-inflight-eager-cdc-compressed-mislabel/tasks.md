## 1. Confirm RED (already on branch)

- [x] 1.1 Run `go test ./pkg/cache/ -run '^TestGetNar_InFlightEagerCDC_CompressedRequestNotMislabeled$' -count=1 -v` and confirm it FAILS with `expected "xz" actual "none"` + `xz: invalid header magic bytes`
- [x] 1.2 (optional, heavy) Run `nix run .#e2e -- --mode local --scenario input-compression` and confirm it FAILs with `mislabeled > 0`

## 2. Implement the in-flight compression guard

- [x] 2.1 In `pkg/cache/cache.go` `GetNar`, in the `case <-ds.start:` block, before the relabel (`narURL.Compression = ds.tempFileCompression`, ~line 1459), call `compressedRequestNeedsUpstreamFallback(requestedCompression, ds.tempFileCompression)`
- [x] 2.2 When that predicate is true (a non-matching compressed request — xz or zstd — the uncompressed in-flight holder cannot satisfy), `ds.wg.Done()` and return `storage.ErrNotFound` so `GetNar`'s caller falls back to upstream — do NOT stream the uncompressed temp bytes mislabeled
- [x] 2.3 When the predicate is false (none request, or a compressed request matching the temp's own compression), keep the current behavior unchanged (relabel to `ds.tempFileCompression` and stream; the existing decompress-on-the-fly goroutine still handles a none request from a compressed temp)
- [x] 2.4 Verify the new early-return path releases `ds.wg` exactly once (no leak/double-Done) and records an appropriate metric attribute, consistent with the sibling `ctx.Done()` / `getError()` returns

## 3. Verify GREEN + no regressions

- [x] 3.1 Re-run 1.1 — the unit test now PASSES (xz request 404s; none unchanged). Also caught + fixed a regression: `nar_does_not_exist_upstream` must surface `upstream.ErrNotFound`, so the fallback is checked only after `ds.getError()`
- [x] 3.2 Run `go test ./pkg/cache/...` — full package green (no truncation/termination/recompression regressions)
- [x] 3.3 Run `nix run .#e2e -- --mode local --scenario input-compression` — now PASSES (`mislabeled: 0`, `404(fallback): 16048`, window overlapped True)
- [x] 3.4 Run `task fmt`, `task lint`, `task test` and confirm each exits 0 (lint needed a stale cross-worktree golangci cache clean)

## 4. Sync + archive

- [x] 4.1 `/opsx:sync` the `nar-concurrent-streaming` delta into `openspec/specs/`
- [x] 4.2 `/opsx:archive` the change once implementation + verification are complete
