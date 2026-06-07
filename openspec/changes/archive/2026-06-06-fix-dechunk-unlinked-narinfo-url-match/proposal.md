## Why

The de-chunk path identifies the narinfos that reference a NAR with a raw SQL predicate: `Or(HasNarInfoNarFilesWith(join), URLHasPrefix("nar/"+hash+"."))`. The prefix branch only matches **canonical** `nar/<hash>...` URLs. A nix-serve-style **prefixed** URL — `nar/<narinfoHash>-<hash>.nar.xz` — starts with `nar/<narinfoHash>-`, so the `nar/<hash>.` prefix never matches it. When such a narinfo is also **unlinked** (a known race drops the `narinfo_nar_files` join row), neither branch matches, so:

- `MigrateChunksToNar` / `NormalizeChunkedNarInfoURL` leave the narinfo advertising a stale `.nar.xz` URL after the NAR is de-chunked to whole-file → a later serve 404s (serve-time normalization only rewrites while chunks exist).
- `LinkedNarinfoNarHash` wrongly concludes there is no `NarHash` to verify against.

This is reachable: `checkAndFixNarInfosForNar` already documents that CDC row replacement can drop the join row.

## What Changes

- The three de-chunk sites that locate referencing narinfos (`MigrateChunksToNar`, `NormalizeChunkedNarInfoURL`, `LinkedNarinfoNarHash`) SHALL identify a referencing narinfo **hash-aware**: parse/normalize each candidate URL's embedded NAR hash and match it against the target hash, instead of a raw `URLHasPrefix` match.
- As a result, an unlinked narinfo with a nix-serve-style prefixed URL (`nar/<narinfoHash>-<hash>.nar.xz`) SHALL be found — so its verification NarHash is resolved and its URL is normalized to `none` on de-chunk.
- The join-link branch and canonical-URL matches continue to work unchanged.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `chunks-to-nar-migration`: strengthen the "resolve verification NarHash via the narinfo URL when no join link exists" and "de-chunking MUST normalize the narinfo URL to none" requirements so the URL-based narinfo lookup is hash-aware and covers unlinked nix-serve-style prefixed URLs.

## Non-goals

- No change to the join-link (primary) match path or to verified-or-nothing reconstruction.
- No change to how prefixed URLs are produced by nix-serve-style upstreams.
- No backfill/repair migration for already-stranded narinfos; the corrected de-chunk pass fixes them on its next run over the affected hash.

## Impact

- **Code**: `pkg/cache/cache.go` — the three `URLHasPrefix("nar/"+narURL.Hash+".")` sites (in `MigrateChunksToNar`, `NormalizeChunkedNarInfoURL`, `LinkedNarinfoNarHash`) replaced with a hash-aware match (a shared predicate/helper reusing the existing URL parse/normalize logic).
- **Database**: none (no schema/migration change).
- **I/O / network / memory**: negligible. The hash-aware match may require fetching candidate narinfo rows and parsing their URLs in Go rather than a single prefix-filtered `UPDATE`; bounded by the small number of narinfos referencing one hash. No change to the hot serve path.
