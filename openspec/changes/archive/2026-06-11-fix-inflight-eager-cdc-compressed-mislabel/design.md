## Context

`Cache.GetNar` (`pkg/cache/cache.go`) serves a NAR from several paths. When the
NAR is not yet local and an upstream download is in flight, it serves via the
**per-client live-streaming** path: it joins the holder's `downloadState` (`ds`)
and streams the holder's temp file.

Under eager CDC the holder downloads the upstream `.nar.xz`, decompresses it into
a temp file holding the raw **uncompressed** NAR (`ds.tempFileCompression =
none`, `cache.go:3300`), and chunks it in the background. `GetNar` captures the
client's `requestedCompression` at entry (`cache.go:1294`) but, on the streaming
branch, does:

```go
case <-ds.start:
    narURL.Compression = ds.tempFileCompression   // cache.go:1459 — becomes none
```

So a `.nar.xz` (or `.nar.zst`) request piggybacking on the in-flight holder is
relabeled `none` and streamed as the decompressed body. `server.go:897` then
sees `Compression == none` and may add transport zstd. The client, told `xz` by
its narinfo, gets `input compression not recognized` (issue #1398). The
finished-chunk and staging paths already handle this:
`compressedRequestNeedsUpstreamFallback` (`inflight_staging_reader.go:298`) and
`serveZstdFromChunks` (`cache.go:3689`). The in-flight streaming branch is the
one gap. `ds.compressedAssetPath` is declared but never assigned, so the
original xz is not retained on disk during the window — serving real xz mid-pull
is not possible.

RED proof exists on this branch: unit
`pkg/cache/input_compression_not_recognized_test.go`; e2e scenario
`input-compression` (`nix/e2e-tests`, `mislabeled: 24`).

## Goals / Non-Goals

**Goals:**
- A compressed request served via the in-flight streaming path is either served
  in the requested compression (none/zstd) or 404s for upstream fallback — never
  a mislabeled 200.
- Reuse the existing fallback predicate and zstd-recompression mechanism; one
  rule shared with the staging path.
- Keep the `none` in-flight serve and all other paths byte-for-byte unchanged.

**Non-Goals:**
- Adding an xz compressor (xz stays a fallback, never recompressed).
- Retaining the original compressed download on disk (`compressedAssetPath`).
- Changing predictive-none narinfo behavior or the proxied-narinfo path.

## Decisions

**D1 — Gate the relabel on `requestedCompression`, in `GetNar`'s streaming
branch.** The fix lives where the bug is: in the `case <-ds.start:` block, before
`narURL.Compression = ds.tempFileCompression` at `cache.go:1459`, consult the
captured `requestedCompression`:
- if `compressedRequestNeedsUpstreamFallback(requestedCompression,
  ds.tempFileCompression)` is true (the in-flight holder representation cannot
  satisfy a non-matching compressed request) → `ds.wg.Done()` and return
  `storage.ErrNotFound`, so `GetNar`'s caller falls back to upstream;
- otherwise → current behavior (`narURL.Compression = ds.tempFileCompression`,
  stream; the existing decompress-on-the-fly goroutine handles a `none` request
  from a compressed temp).

*Alternative considered:* fix it in `server.go` by not adding transport zstd for
a compressed request. Rejected — that only hides the zstd-transport symptom; the
body would still be uncompressed bytes mislabeled `none`, and the client still
fails. The defect is the compression relabel, not the HTTP encoding.

**D2 — Fall back (404) for BOTH xz and zstd from an uncompressed in-flight
holder, rather than recompressing in-flight.** `compressedRequestNeedsUpstreamFallback`
already returns true for both (`!isNone(requested) && requested != available`),
and this is exactly how the in-flight **staging** serve path behaves
(`inflight_staging_reader.go:252`) — so the two in-flight paths stay identical.
ncps cannot produce xz at all; for zstd, in-flight recompression would require
threading a zstd encoder through the complex progressive-streaming loop (real
risk for a bugfix), and a racing zstd request 404ing simply triggers one upstream
fallback fetch. Crucially this is **only the brief in-flight window**: once the
NAR is fully chunked, a `zstd` request is served by `serveZstdFromChunks`
(`nar-serving-recompression`), so steady-state zstd serving is unchanged.
*Alternative:* recompress zstd in-flight (mirror `serveZstdFromChunks`).
Deferred as an optimization — higher risk, negligible benefit (rare race), and
not needed to fix #1398.

**D3 — Reuse `compressedRequestNeedsUpstreamFallback`** rather than new logic, so
the in-flight streaming path and the in-flight staging path share one fallback
rule and cannot diverge.

## Risks / Trade-offs

- [A racing xz request now 404s instead of returning corrupt 200] → This is the
  intended, already-graceful behavior (client falls back to upstream). It trades
  a guaranteed client failure for one extra upstream fetch on the racing
  request. Net strictly better.
- [zstd recompression cost on the in-flight path] → Same streaming, pooled-encoder
  mechanism already used by `serveZstdFromChunks`; O(1) memory, no buffering.
  Only incurred for zstd requests that race the window (rare).
- [Behavior change could regress the `none` in-flight serve] → Guarded: the
  `none` branch is unchanged; the unit + e2e tests assert byte-identity and the
  existing `nar-concurrent-streaming` truncation/termination scenarios still
  apply.

## Migration Plan

No schema, config, or API change. Pure serve-path logic. Deploy is a binary
roll; rollback is reverting the binary. Forward/backward compatible with peers
(no shared-state contract change).

## Open Questions

- Should the proxied-narinfo path have normalized to predictive-none here (so
  clients never request xz in the first place)? Out of scope for this change;
  tracked separately. This change makes the serve path correct regardless of how
  the client obtained an xz-advertising narinfo.
