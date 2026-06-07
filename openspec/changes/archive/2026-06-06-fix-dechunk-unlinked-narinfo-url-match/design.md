## Context

When a NAR is de-chunked to whole-file (`Compression:none`) storage, the de-chunk path must (a) resolve the verification NarHash from a referencing narinfo and (b) rewrite every referencing narinfo's URL to `nar/<H>.nar`. Both depend on locating the narinfos that reference the NAR. Three sites in `pkg/cache/cache.go` do this with the same predicate:

```go
entnarinfo.Or(
    entnarinfo.HasNarInfoNarFilesWith(entnarinfonarfile.NarFileIDEQ(nr.ID)), // join link (primary)
    entnarinfo.URLHasPrefix("nar/"+narURL.Hash+"."),                          // URL fallback
)
```

- `MigrateChunksToNar` (the flip transaction — normalizes URLs)
- `NormalizeChunkedNarInfoURL`
- `LinkedNarinfoNarHash` (resolves the verification NarHash)

The URL fallback exists to cover **unlinked** rows (a known race drops the `narinfo_nar_files` link). But `URLHasPrefix("nar/"+H+".")` only matches URLs whose path component immediately after `nar/` is the NAR hash. A nix-serve-style prefixed URL embeds a narinfo-hash prefix: `nar/<narinfoHash>-<H>.nar.xz`. That string starts with `nar/<narinfoHash>-`, so the `nar/<H>.` prefix never matches. Unlinked + prefixed therefore falls through both branches.

The project already has URL parsing/normalization that strips the narinfo-hash prefix: `nar.URL.Normalize()` (and `ParseURL`) reduce `nar/<narinfoHash>-<H>.nar.xz` to hash `H`. The fix is to use that, hash-aware, instead of a textual prefix.

## Goals / Non-Goals

**Goals:**
- An unlinked narinfo with a nix-serve-style prefixed URL is recognized as referencing the NAR.
- All three sites share one hash-aware identification, so they cannot drift.
- Join-link and canonical-URL matches keep working.

**Non-Goals:**
- Changing the join-link primary path or verified-or-nothing reconstruction.
- A repair migration for already-stranded narinfos (the corrected pass fixes them on next run).

## Decisions

**1. Match by normalized embedded hash, not by URL prefix.**
Replace the `URLHasPrefix` branch with a hash-aware match: identify a candidate narinfo as referencing the NAR when `Normalize(ParseURL(narinfo.URL)).Hash == narURL.Hash`. Because a prefixed URL cannot be reduced to its hash in pure SQL, the fallback fetches candidate narinfos and evaluates the embedded hash in Go, then operates on the matched set.
*Alternatives considered*: a broader SQL `LIKE %<H>.%` (rejected — can match unrelated hashes / different NARs, losing the "trailing dot anchors the fixed-length hash" safety the current code relies on); a generated normalized-hash column on `narinfos` (rejected — schema/migration cost for a narrow race-recovery path).

**2. One shared helper for all three sites.**
Extract the hash-aware "does this narinfo reference NAR H" logic into a single helper so `MigrateChunksToNar`, `NormalizeChunkedNarInfoURL`, and `LinkedNarinfoNarHash` use identical semantics.

**3. Keep the join link as the primary, cheap path.**
Try the join link first; only fall back to the hash-aware URL scan when no link exists, preserving today's fast path and only paying the scan for the unlinked race.

## Risks / Trade-offs

- [Fetching + parsing candidate narinfos costs more than a prefix `UPDATE`] → bounded by the few narinfos referencing one hash, and only on the unlinked fallback; the hot path (join link) is unchanged.
- [A malformed/unparseable narinfo URL] → treated as non-matching (same as today's prefix miss), never as a false match; reconstruction stays verified-or-nothing.
- [Behavioral overlap with the existing "resolved by NAR hash from any referencing narinfo" scenario] → that scenario covers canonical different-compression URLs (`nar/<H>.nar.xz`); this change additionally covers the prefixed form (`nar/<narinfoHash>-<H>...`). The hash-aware helper subsumes both.

## Migration Plan

Pure code change; no schema or migration. Deployable immediately. Rollback is reverting the helper + three call sites. Stranded narinfos from the old behavior self-correct the next time the de-chunk pass processes their hash.

## Open Questions

None.
