## Context

CDC stores NARs as uncompressed chunks; the canonical narinfo is normalized to `Compression: none`. But an upstream (e.g. cachix, gepbird's cache) serves `Compression: zstd`, and that narinfo can be served/retained advertising `zstd` while the local representation is uncompressed chunks. In `serveNarFromStorageViaPipe` (`pkg/cache/cache.go`), `serveFromChunks` is true (no whole file in store, chunk store available); the guard at ~3579 then returns `ErrNotFound` for any non-`none` compression:

```go
if serveFromChunks && narURL.Compression != nar.CompressionTypeNone {
    return 0, nil, fmt.Errorf("NAR %s is only available as chunks, cannot serve as %s: %w", ...)
}
```

`getNarFromChunks` reassembles the uncompressed NAR. `pkg/zstd` provides a pooled streaming writer (`NewPooledWriter`), already used to store `none` NARs as `.nar.zst` (cache.go:2186). There is **no** xz compressor in ncps (`pkg/xz` only decompresses).

## Goals / Non-Goals

**Goals:**
- Serve a `Compression: zstd` request from uncompressed CDC chunks by recompressing on the fly, labeled `zstd`, instead of 404.
- Stream (no full-NAR buffering); reuse the existing pooled zstd writer and the `io.Pipe` + `analytics.SafeGo` pattern already used in `serveNarFromStorageViaPipe`.
- Preserve all existing paths: `none` from chunks (progressive/decompressed), zstd from a stored `.nar.zst` whole file (as-is), and the 404 for compressions we cannot produce.

**Non-Goals:**
- xz recompression (no compressor; expensive). An `xz`-advertised request with only an uncompressed/chunk representation keeps returning `ErrNotFound`; the write-path invariant and data-repair follow-ups ensure ncps does not advertise xz it cannot serve.
- Changing what compression is advertised (that is the write-path-invariant change).
- The in-flight staging reader's compressed-from-uncompressed fallback (`compressedRequestNeedsUpstreamFallback`) — separate path, unchanged here.

## Decisions

- **Recompress only for zstd; keep the 404 for other codecs.** Gate the new path on `narURL.Compression == zstd`. This solves the dominant inverse case (gepbird/cachix) with a fast compressor and leaves the un-producible codecs to the higher-level invariant/repair work. *Alternative:* add an xz compressor — rejected: no streaming xz writer wrapper, high CPU per serve, and the better fix for xz is to not advertise it.
- **Reassemble via the uncompressed chunk path, then wrap in a zstd pipe.** Call `getNarFromChunks` with the `none` URL to get the raw stream, then `io.Copy` it through a `zstd.NewPooledWriter` into an `io.Pipe`, returning the pipe reader labeled `zstd` with size `-1` (compressed size unknown up front). *Alternative:* compress to a temp file to get a Content-Length — rejected: adds disk IO and latency; chunked transfer is already used elsewhere for `size == -1`.
- **Report the served compression as `zstd`.** The returned `narURL.Compression` stays `zstd` so the response is labeled correctly (mirrors the lesson from `TestCDCWholeFileServeReportsServedCompression`: never mislabel the served stream).

## Risks / Trade-offs

- **Serve-time CPU for recompression** → bounded to the compressed-request-from-chunks path; zstd is fast and pooled. Net win vs a 404 that aborts the build. Mitigation: only zstd (cheap), streaming (no buffering).
- **Unknown Content-Length (`size = -1`)** → chunked transfer; already supported by the HTTP layer and used by the decompress paths. No client-visible regression.
- **Double work if both a stored `.nar.zst` whole file and chunks exist** → not triggered: `serveFromChunks` is only true when no whole file is in store; a stored `.nar.zst` is served as-is by the existing path.

## Migration Plan

Behavior-only change in `pkg/cache`. No schema/migration/config/API change. Roll the image; revert to roll back.

## Open Questions

- None blocking. xz-advertised-from-uncompressed is intentionally deferred to the write-path-invariant + data-repair changes in this stack.
