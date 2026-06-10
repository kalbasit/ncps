## Why

A client whose narinfo advertises `Compression: zstd` requests `/nar/<hash>.nar.zst`, but the NAR is stored only as uncompressed CDC chunks. The serve path cannot produce a compressed stream from chunks, so it returns 404 ("NAR is only available as chunks, cannot serve as zstd") and the build aborts with `does not exist in binary cache`. This is the steady-state-CDC manifestation of the narinfo↔nar_file compression desync independently reported on a CDC-on deployment (issue #1392), and the inverse direction of the already-merged uncompressed-serve fix (#1393). zstd is the common upstream compression (cachix, gepbird's cache), so this blocks CDC-on caches.

## What Changes

- When a `Compression: zstd` request can only be served from uncompressed CDC chunks (or an uncompressed whole file), the serve path SHALL reassemble the uncompressed bytes and recompress them to zstd on the fly, serving a correctly-labeled `zstd` stream instead of returning 404.
- Recompression is scoped to **zstd** (a fast streaming compressor already vendored, `pkg/zstd`). Other compressed requests we cannot cheaply produce (notably `xz`, which has no compressor in ncps) keep the existing 404/fallback behavior — those are addressed by the write-path invariant and data-repair follow-ups so ncps never advertises a compression it cannot serve.
- A regression test reproduces the failure: chunk a NAR under CDC, request it as `zstd`, and assert the served stream decompresses (zstd) to the original NAR (was 404).

## Capabilities

### New Capabilities
- `nar-serving-recompression`: Serving a `Compression: zstd` NAR request by recompressing the reassembled uncompressed bytes (from CDC chunks or an uncompressed whole file) on the fly, so a NAR present only in an uncompressed representation is served in the advertised compression instead of 404'd.

### Modified Capabilities
<!-- none -->

## Impact

- `pkg/cache/cache.go`: `serveNarFromStorageViaPipe` (the `serveFromChunks && compression != none` branch at ~3579) gains a zstd recompression path; a small zstd-compress pipe helper.
- New test in `pkg/cache`.
- Behavior-only; no schema/migration/config/API change. Adds serve-time CPU for zstd recompression of chunk-served NARs (bounded; only on the compressed-request-from-chunks path).
