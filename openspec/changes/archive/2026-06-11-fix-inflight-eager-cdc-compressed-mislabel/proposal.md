## Why

GitHub issue #1398: under eager CDC, a client requesting a compressed NAR
(`/nar/<h>.nar.xz` or `.nar.zst`) while that NAR is mid pull-through gets an
HTTP 200 whose body is the **uncompressed** NAR, relabeled `Compression: none`.
The client — told `xz` by its narinfo — cannot decode it and fails with
`input compression not recognized`. The in-flight live-streaming path
(`GetNar` → temp-file serve) overwrites the requested compression with the
holder temp file's compression (`none`) and streams the decompressed bytes,
ignoring what the client asked for. The finished-chunk serve paths already get
this right (`nar-serving-recompression`); the in-flight path does not.

## What Changes

- Before the in-flight temp-file serve relabels the request to the holder's
  compression, it consults the **originally requested** compression and applies
  the same discipline the in-flight staging path already uses:
  - `none` request → serve the decompressed temp bytes as today (unchanged).
  - any non-matching compression the uncompressed in-flight holder cannot
    satisfy (`zstd` *or* `xz`) → return `storage.ErrNotFound` (HTTP 404) so the
    client falls back to an upstream that still has the original file — never a
    mislabeled body. This folds the fatal mislabel into the already-graceful
    404-fallback the post-window path produces.
  - This is only the brief in-flight window: once the NAR is fully chunked, a
    `zstd` request is served by recompression (`serveZstdFromChunks` /
    `nar-serving-recompression`) and a `none` request by reassembly — unchanged.
- Reuse the existing `compressedRequestNeedsUpstreamFallback` predicate so the
  in-flight path and the staging path share one fallback rule.

## Capabilities

### New Capabilities
- (none)

### Modified Capabilities
- `nar-concurrent-streaming`: add a requirement that the per-client in-flight
  live-streaming path MUST serve the client's requested compression (none
  as-is, zstd recompressed) or fall back with not-found — it MUST NOT emit a
  200 whose body is mislabeled as a different compression than the client
  requested.

## Non-goals

- No change to finished-chunk / whole-file serve paths (`nar-serving-*`), which
  already behave correctly.
- No xz compressor: ncps still cannot produce xz; xz requests fall back to
  upstream rather than being recompressed.
- Not fixing why the proxied narinfo advertised xz instead of predictive-none
  here (a possible separate concern, tracked separately).
- No on-disk retention of the original compressed download (`compressedAssetPath`
  stays unused).

## Impact

- Code: `pkg/cache/cache.go` (`GetNar` in-flight temp-file serve branch, ~line
  1455-1499). Tests already RED on this branch: unit
  `pkg/cache/input_compression_not_recognized_test.go`; e2e scenario
  `input-compression` (`nix/e2e-tests`).
- Network: a compressed (xz) request landing in the in-flight window now 404s →
  one upstream fallback fetch for that request (same as the post-window path
  already does), instead of a corrupt 200. No new memory; the zstd path streams
  through a pooled encoder (O(1) memory), no extra buffering. No DB/migration
  impact.
