## Why

`cache.snix.dev` — a configured upstream — serves narinfos whose `URL:` field is a
snix-castore blob reference, `nar/snix-castore/<base64-protobuf>?narsize=N`, with
`Compression: none` and **no `.nar` extension**. ncps's opaque-URL tolerance
(`nar.ParseUpstreamURL`) still requires a `.nar` token, so `parseURLParts` rejects
these with `ErrInvalidURL`, and the `<hash>.narinfo` request returns **HTTP 500
"invalid nar URL"**. The failure is intermittent — upstream selection races all
healthy upstreams and the first responder wins, so a retry that lands on
cache.nixos.org succeeds — which makes it a confusing, hard-to-diagnose prod error
(confirmed in production logs).

## What Changes

- Generalize opaque upstream NAR URL handling so a narinfo `URL:` that carries **no
  `.nar` token** is treated as opaque rather than rejected: preserve the full path
  **and query string** verbatim for the upstream GET, derive the local storage key
  from `NarHash`, and treat compression as `none` (taken from the narinfo, not the
  URL extension).
- Re-serve such NARs to downstream clients under ncps's own hash-named
  `nar/<narhash>.nar` (`Compression: none`), reusing the existing opaque-path
  persistence so the NAR is re-fetchable from the original upstream after eviction.
- The upstream GET MUST retain the `?narsize=N` query (snix returns `400` without it).
- Update `CHANGELOG.md` and `docs/` to document snix-castore / opaque-URL support.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `upstream-fetch-resilience`: broaden the "Opaque (non hash-named) upstream NAR
  URLs MUST be tolerated" requirement to cover URLs with **no `.nar` token** (e.g.
  snix-castore), including preserving the query string on the upstream GET and
  sourcing compression from the narinfo `Compression:` field.

## Non-goals

- Decoding or interpreting the tvix/snix-castore protobuf blob. ncps follows the
  Nix HTTP binary-cache protocol, where the narinfo `URL:` is an **opaque path
  relative to the cache root**; a pull-through proxy never needs castore internals.
- Native castore storage, a new storage format, or any new configuration flag.
- Changing upstream **selection** ordering or adding parse-failure fall-through
  between upstreams (a separate resilience concern; out of scope here).
- Supporting compressed opaque URLs that lack a `.nar` token (none observed;
  snix-castore is always `Compression: none`).

## Impact

- **Code**: `pkg/nar/url.go` (`parseURLParts` / `ParseUpstreamURL`), `pkg/cache/cache.go`
  (narinfo pull + re-advertise path). No schema/migration change — opaque-path
  persistence (`upstream_url`) already exists.
- **Docs**: `CHANGELOG.md`, `docs/` (Request Flow / upstream handling).
- **I/O / network / memory**: negligible. Same single upstream GET, same
  uncompressed-NAR handling already used for cachix opaque URLs; no extra
  round-trips, no added buffering.
