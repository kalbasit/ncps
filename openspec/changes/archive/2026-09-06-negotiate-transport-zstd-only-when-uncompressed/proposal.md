## Why

`upstream.GetNar` sends `Accept-Encoding: zstd` on **every** upstream NAR fetch and transparently strips any `Content-Encoding: zstd` it gets back. For a NAR the narinfo already declares compressed, that request buys close to nothing — zstd over zstd/xz is wasted CPU on both ends — and it is actively harmful: a compressing proxy in front of the upstream (Caddy's `encode zstd`, nginx, a CDN) honours the request and wraps the body. If the upstream's own content was not in fact compressed, ncps strips the encoding, is left holding a **raw** NAR, and goes on to store and serve it as `Compression: zstd`; nix then fails with `input compression not recognized`.

ncps is uniquely exposed because it *always* advertises zstd while nix's own curl frequently is not built with it — so such a proxy compresses for ncps alone, and the same store path that substitutes fine directly fails through ncps. This gap was found while diagnosing #1470, confirmed not to be that reporter's failure mode, and left behind as a skipped test.

## What Changes

- Request `Accept-Encoding: zstd` from an upstream only when the NAR's resolved compression is `none`. A NAR already declared `zstd`/`xz`/etc. is fetched without transport negotiation, so there is nothing for a proxy to wrap and ncps keeps the upstream's own bytes.
- Transparent stripping of `Content-Encoding: zstd` is unchanged — it remains correct HTTP behaviour for any response that carries one.
- Un-skip the transport-level subtest in `pkg/cache/attic_narinfo_url_compression_test.go` and make its fake upstream model a real proxy (apply the encoding only when the client advertises it).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `upstream-fetch-resilience`: adds a requirement governing when ncps negotiates transport-level zstd on an upstream NAR fetch. The capability already owns upstream NAR GET behaviour (opaque URLs, retries, compression resolution).

## Non-goals

- **Does not** change the downstream/server side. Client-facing `Accept-Encoding` handling and transparent re-compression (`api-surface`, `architecture`) are untouched.
- **Does not** change transparent decompression of a `Content-Encoding: zstd` response. Such a response is still decoded whether or not the system negotiated it — decoding a declared transfer encoding is correct regardless. What stays unsupported is narrower and is about the *decoded* body: if that body then fails to match the compression the narinfo declares, the system does not detect or repair the mismatch. This change removes the case where ncps itself invited that mismatch.
- **Does not** revisit the narinfo `Compression:` resolution from #1470, which this depends on: `narURL.Compression` is only trustworthy because of it.
- **Does not** add sniffing or heuristics to detect a lying upstream.

## Impact

- Code: `pkg/cache/upstream/cache.go` (`GetNar`). Single call site, `pkg/cache/cache.go` `getNarFromUpstream`.
- Spec: `openspec/specs/upstream-fetch-resilience/spec.md`.
- Network: slightly **more** bytes on the wire in the rare case an upstream was usefully double-compressing an already-compressed NAR; realistically unchanged, since compressing compressed data yields ~0%. Uncompressed NARs (Harmonia) keep the negotiation and are unaffected.
- CPU: reduced on both ends — one fewer compress/decompress pass per compressed-NAR fetch.
- Memory: unchanged. Data: no migration.
