## Why

ncps derives a NAR's compression exclusively from the file extension on the narinfo `URL:` field and never reconciles it against the narinfo's own `Compression:` header. Attic states compression *only* in the header — its URL is always a bare `nar/<storePathHash>.nar` with `file_hash`/`file_size` unset — so ncps reads `none`, links a `Compression: zstd` narinfo to a `compression=none` nar_file, and re-compresses the already-compressed NAR a second time on ingest (issue #1470). The narinfo ncps re-signs and serves therefore contradicts its own stored bytes.

## What Changes

- Resolve a NAR's compression from the narinfo `Compression:` header when the upstream `URL:` carries no compression extension; the URL extension remains authoritative when present. A conflict between an explicit extension and the header keeps today's URL-derived behaviour.
- Apply the resolved compression consistently across the narinfo rewrite, the `nar_file` row, and the storage encoding decision, so all three agree.
- Stop the double-compression: a NAR whose content is already compressed is stored under its own compression rather than re-compressed as `.nar.zst`.
- Emit a truthful URL: the narinfo ncps re-serves carries the resolved compression extension, so a client requesting it receives bytes matching the advertised `Compression:`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `upstream-fetch-resilience`: the opaque-URL requirement currently mandates `Compression: none` for a URL with no compression extension. It changes to: the narinfo `Compression:` header is authoritative when the URL carries no extension, for both opaque shapes and conventional hash-named ones.

## Non-goals

- **Does not** change ncps's `Accept-Encoding: zstd` upstream request or its transparent `Content-Encoding` stripping. An upstream that states zstd at the transport level over an uncompressed body still yields a raw NAR labelled `zstd`; that is a separate defect and a separate change.
- **Does not** repair narinfo/nar_file rows already written with the wrong compression. Existing drift is `narinfo-compression-repair`'s (fsck) territory.
- **Does not** alter the serve path, CDC, or chunk handling.
- **Does not** change behaviour for upstreams that put the compression in the URL (cache.nixos.org, cachix, Harmonia, nix-serve).

## Impact

- Code: `pkg/nar/url.go` (`ParseUpstreamURL` compression resolution) and its two callers in `pkg/cache/cache.go` (`pullNarInfo`'s normalization switch and `lookupPreferredUpstreamURL`). `storeInDatabase` and `putNarInStore` become correct without modification once the resolved compression reaches them.
- Spec: `openspec/specs/upstream-fetch-resilience/spec.md`.
- I/O and CPU: strictly reduced for affected upstreams — one fewer zstd compression pass on ingest and a smaller on-disk file, since already-compressed NARs stop being wrapped a second time. No change for unaffected upstreams.
- Network: unchanged. Memory: unchanged (compression stays streaming through the existing pooled zstd writers).
- Data: no migration. New ingests become self-consistent; pre-existing rows are untouched.
