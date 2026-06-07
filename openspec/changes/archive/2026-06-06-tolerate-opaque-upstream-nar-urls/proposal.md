## Why

Some upstream binary caches (notably cachix) publish narinfo `URL:` fields that are **not** hash-named — e.g. `nar/<uuidv4>.nar.zst`. ncps previously parsed every upstream NAR URL by extracting a Nix hash from the filename, so these opaque URLs failed hash validation and the proxy returned HTTP 500 (`invalid nar hash`), making such caches unusable through ncps.

## What Changes

- ncps SHALL tolerate opaque (non hash-named) upstream NAR URLs instead of rejecting them. The original upstream path is preserved verbatim for the upstream GET, while ncps's local storage key is derived from the narinfo `NarHash`.
- When the upstream URL is opaque, ncps re-serves the NAR under its **own** hash-named URL (keyed off `NarHash`), so downstream Nix clients always see conventional hash-named URLs.
- The opaque upstream path is persisted in a new nullable `narinfos.upstream_url` column so the NAR can be re-fetched from upstream after the local copy is evicted (the served narinfo URL no longer encodes the opaque path).
- On a cache-miss for an evicted NAR, ncps restores the persisted opaque path so the upstream GET targets the original location.
- Conventional hash-named upstream URLs are unaffected (fast path identical to prior behavior).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `upstream-fetch-resilience`: add a requirement that opaque (non hash-named) upstream NAR URLs are tolerated — keyed off `NarHash`, re-served under ncps's own hash-named URL, with the opaque path persisted for post-eviction re-fetch.
- `data-model`: add the nullable `upstream_url` column to the `narinfos` table schema.

## Non-goals

- No change to how conventional hash-named upstream URLs are handled.
- No change to the downstream (client-facing) URL scheme — clients always receive hash-named URLs.
- No backfill of `upstream_url` for narinfos cached before this change; the column stays NULL and those NARs were already hash-named.
- No new configuration flags; the behavior is always on.

## Impact

- **Code**: `pkg/nar/url.go` (`ParseUpstreamURL`, opaque-path accessors), `pkg/cache/cache.go` (pull/serve/re-fetch paths), `ent/schema/narinfo.go` (new field).
- **Database**: additive forward-only migration adding `narinfos.upstream_url` (sqlite/postgres/mysql).
- **I/O / network / memory**: negligible. No extra upstream requests on the happy path; one extra best-effort single-row `UPDATE` per opaque-URL narinfo at pull time; one extra nullable text column read per narinfo lookup. No change to NAR streaming or buffering.
