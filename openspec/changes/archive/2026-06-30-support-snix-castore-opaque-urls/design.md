## Context

ncps proxies several upstream binary caches. The Nix HTTP binary-cache protocol
treats a narinfo's `URL:` field as an **opaque path relative to the cache root**;
clients and pull-through proxies must not assume it is `nar/<hash>.nar[.<comp>]`.

ncps already tolerates *some* non-conventional URLs: `nar.ParseUpstreamURL` accepts
filenames whose stem is not a valid Nix hash (cachix's `nar/<uuid>.nar.zst`),
preserving the path for the upstream GET and keying local storage off `NarHash`.
But `parseURLParts` still hard-requires a `.nar` token — it does
`strings.Cut(filename, ".nar")` and returns `ErrInvalidURL` when absent.

`cache.snix.dev` (a configured upstream) serves snix-castore URLs that have **no**
`.nar` token:

```
URL: nar/snix-castore/CiUSIATh-lHQ2Dp92sJJfOGg0-s8mLizwHc0z3OtQ953fwSUGNoC?narsize=7415800
Compression: none
NarHash: sha256:0cr6df6...   NarSize == FileSize == 7415800
```

Fetching that path returns `HTTP 200/206`, `Content-Type: application/x-nix-nar`, a
plain uncompressed NAR (verified). The `?narsize=N` query is **required** — snix
returns `400` without it. Because `parseURLParts` rejects the URL, the `.narinfo`
request 500s ("invalid nar URL"). It is intermittent: upstream selection races all
healthy upstreams, so a retry landing on cache.nixos.org (conventional URL) succeeds.

## Goals / Non-Goals

**Goals:**
- Proxy snix-castore (and any `.nar`-less opaque) upstream narinfo URLs instead of
  500-ing, following the opaque-URL contract already spec'd for cachix.
- Preserve the full upstream path **and query string** for the upstream GET.
- Key local storage off `NarHash`; treat compression as `none`.

**Non-Goals:**
- Decoding the tvix/snix-castore protobuf blob (never needed by a proxy).
- Native castore storage, new storage formats, or new config flags.
- Changing upstream selection or adding cross-upstream parse-failure fall-through.
- Compressed `.nar`-less opaque URLs (unobserved; snix-castore is always `none`).

## Decisions

**D1 — Recover in `ParseUpstreamURL`, not `ParseURL`.** Only the upstream-facing
parser gains `.nar`-less tolerance; the strict `ParseURL` (used for ncps's own
serve/storage keys) stays unchanged, so ncps still only ever *emits* hash-named
URLs. *Alternative rejected:* loosening `parseURLParts` globally would let malformed
URLs through the serve path.

**D2 — Detect the `.nar`-less case explicitly and build an opaque URL.** When the
filename has no `.nar` token, `ParseUpstreamURL` treats the whole path (query
stripped) as the `opaquePath`, parses and retains the query into `URL.Query`, sets
`Compression = none`, and uses `fallbackHash` (`NarHash`) as the storage key —
exactly the existing opaque mechanism, minus the `.nar` requirement. A `.nar`-less
URL with no valid `NarHash` still errors (never fabricate a key). *Alternative
rejected:* a snix-specific `nar/snix-castore/` prefix match — brittle and narrower
than the protocol's opaque-path guarantee.

**D3 — Compression comes from the narinfo, defaulted to `none`.** The URL carries no
compression extension, and snix advertises `Compression: none` with
`FileSize == NarSize`. The `.nar`-less opaque URL therefore parses as `none`; the
existing `narInfo.Compression == none` branch in the pull path already re-advertises
`nar/<narhash>.nar` (`none`) and nulls `FileHash`/`FileSize`, so re-serving needs no
new logic once parsing succeeds.

**D4 — Reuse opaque-path persistence.** The preserved opaque path (incl. query) is
persisted via the existing `upstream_url` mechanism so an evicted NAR is re-fetched
from snix, not from ncps's local-only hash-named URL. No schema/migration change.

## Risks / Trade-offs

- **Dropping the `?narsize=N` query → upstream `400`.** → Test that the query
  round-trips `ParseUpstreamURL` → `JoinURL` into the reconstructed upstream GET.
- **Switch-case ordering (`none` before `IsOpaque`) skips opaque-path persistence.**
  → Test opaque-`none` re-fetch after eviction; ensure `upstreamNarPath` is captured
  before the compression branch and persisted regardless of branch taken.
- **Over-broadening: genuinely malformed URLs slip through as opaque.** → Only
  recover when the path is non-empty *and* `fallbackHash` is valid; empty URL and
  bad-query cases still return `ErrInvalidURL`.
- **`none` opaque NAR storage/serve path is less exercised than compressed opaque.**
  → Integration test the full snix pull → store → serve → re-serve loop.

## Migration Plan

Pure code change; no DB migration, no config, no data backfill. Deploy via normal
rolling update; rollback = revert the commit. Note: current production runs an old
unmerged feature-branch image — this fix must ship from a clean `main` build. No
behavior change for conventional or cachix-style opaque URLs.

## Open Questions

- Should ncps still hard-fail the request when a *selected* upstream's narinfo URL
  is unparseable, or fall through to the next upstream? Deferred to a separate
  upstream-resilience change; out of scope here.
