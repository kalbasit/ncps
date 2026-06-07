## Context

ncps parses every upstream NAR URL by extracting a Nix hash from the filename before `.nar`. The Nix binary-cache protocol, however, treats the narinfo `URL:` field as an **opaque path relative to the cache root** — it is not required to be hash-named. cachix serves NARs at `nar/<uuidv4>.nar.zst`, so ncps's hash extraction failed `ValidateHash`, surfaced as HTTP 500 (`invalid nar hash`), and made cachix-style caches unusable through the proxy.

ncps already keys its local storage off a single Nix hash (`URL.Hash` → `ToFilePath`/`FilePath`). The narinfo independently carries a `NarHash`, which is always a valid Nix hash. That gives a natural fallback storage key when the URL filename is not itself a hash.

## Goals / Non-Goals

**Goals:**
- Proxy upstreams whose NAR URL is opaque, keying local storage off `NarHash`.
- Keep the downstream (client-facing) URL hash-named and conventional.
- Survive eviction: re-fetch an opaque NAR from its original upstream path.
- Zero behavior change and zero added cost on the conventional hash-named path.

**Non-Goals:**
- No new config flags (always-on).
- No backfill of pre-existing rows (they were already hash-named).
- No change to CDC chunking, signature handling, or the downstream URL scheme.

## Decisions

**1. Separate "upstream GET path" from "local storage key" inside `nar.URL`.**
Add an unexported `opaquePath` field to `nar.URL`, populated only by the new `ParseUpstreamURL(u, fallbackHash)`. `Hash` continues to drive local storage (`ToFilePath` stays `FilePath(u.Hash, …)`); `opaquePath`, when set, drives only the upstream request path (`pathWithCompression` → `JoinURL`). Accessors: `IsOpaque()`, `OpaquePath()`, `WithOpaquePath()`.
*Alternative considered*: a separate `UpstreamURL` type. Rejected — it would fork every call site that threads a `nar.URL`; a private field localizes the concern.

**2. Two parse entry points sharing one splitter.**
Refactor the URL splitting into `parseURLParts` (no hash policy). `ParseURL` keeps strict hash validation (client-facing, unchanged contract); `ParseUpstreamURL` applies the fallback policy. Keeping `ParseURL` strict avoids loosening validation everywhere the client path relies on it.

**3. Re-serve opaque NARs under ncps's own hash-named URL.**
In `pullNarInfo`, after prefetch, a `switch` rewrites `narInfo.URL` to `nar/<NarHash>.nar[.<ext>]` for the opaque case (preserving compression), so downstream clients always see hash-named URLs. The `none`-compression normalization case is untouched.

**4. Persist the opaque path in `narinfos.upstream_url`.**
A new nullable Ent field on the narinfo schema. After `storeInDatabase`, a best-effort single-row `UPDATE` records the opaque path (caller logs and continues on error — mirrors `migrateNarToChunksCleanup`; a single `UPDATE` is atomic on all three engines, so no transaction). On cache-miss, `lookupOriginalNarURL` restores it via `WithOpaquePath`, so the upstream GET targets the original path.

## Risks / Trade-offs

- **Capturing the opaque path before the URL is rewritten** → snapshot `narURL.OpaquePath()` into a local before the `switch`, and copy `narURL` into `narURLForBG` before the rewrite so the background fetch keeps the opaque path. Covered by the eviction → re-fetch integration test.
- **Calling `String()` on an opaque `nar.URL` would emit the opaque path, not the hash-named URL** → the rewrite constructs a fresh `nar.URL{Hash, Compression, Query}` without `opaquePath` for the persisted `narInfo.URL`. Verified by review.
- **Best-effort persist could lose the opaque path on UPDATE failure** → only affects post-eviction re-fetch of that one NAR (it would 404 and re-pull the narinfo); acceptable, and logged.
- **NarHash collisions across distinct opaque NARs** → impossible in practice: `NarHash` is the content hash of the NAR, so identical keys imply identical content.

## Migration Plan

Additive forward-only migration adding nullable `narinfos.upstream_url` for sqlite/postgres/mysql, generated via the Ent + Atlas workflow (`task migrations:gen`). Expand-only (a nullable `ADD COLUMN`), so it is safe under rolling upgrades and requires no backfill. Rollback is the standard expand-contract policy; the column is harmless if left in place.

## Open Questions

None.
