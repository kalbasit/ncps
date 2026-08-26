## Context

See proposal.md — Why. The mechanics that shape the approach:

`nar.ParseUpstreamURL(u, fallbackHash)` (`pkg/nar/url.go:64`) derives compression
purely from the URL's file extension via `parseURLParts`, which has no access to
the narinfo. It has two callers, both of which already hold the parsed narinfo:
`pullNarInfo` (`cache.go:4276`) and `lookupPreferredUpstreamURL` (`cache.go:1821`).

Downstream, three places consume that compression and are all currently fed the
wrong value for a bare-`.nar` URL:

- `pullNarInfo`'s normalization switch — the `case narURL.IsOpaque()` arm
  (`cache.go:4368`) rewrites the served URL as
  `nar.URL{Hash: narURL.Hash, Compression: narURL.Compression}` under a comment
  claiming it is "preserving compression".
- `storeInDatabase` (`cache.go:5641`) re-parses the *rewritten* `narInfo.URL` with
  the strict `nar.ParseURL` to key the `nar_file` row.
- `putNarInStore` (`cache.go:2237`) re-compresses the body as zstd when it believes
  the compression is `none`.

Because `storeInDatabase` re-derives from the emitted URL rather than from the
resolved value, the emitted URL and the resolved compression must be kept in
agreement — that constraint drives Decision 2.

Attic's URL uses the 32-char store path hash, which fails `ValidateHash`, so it
takes the opaque branch. A conforming upstream could equally serve
`nar/<52-char-narhash>.nar` with `Compression: zstd` and take the non-opaque fast
path, so the fix cannot live in the opaque branch alone.

## Goals / Non-Goals

**Goals:**

- One resolution point for a NAR's compression, so the narinfo, the `nar_file`
  row, and the stored encoding cannot disagree.
- No behaviour change for upstreams that encode compression in the URL.

**Non-Goals:**

- See proposal.md — Non-goals. At the design level, additionally: no change to
  `nar.ParseURL`, the strict parser for ncps's own serve/storage keys. It must keep
  rejecting anything ncps would not itself emit.

## Decisions

**1. Resolve at the parse boundary: `ParseUpstreamURL(u, fallbackHash, declaredCompression)`.**

The function already accepts a narinfo-derived argument (`fallbackHash`) for
exactly this reason — the URL alone is insufficient — so a second one is
consistent rather than novel. Both callers have the narinfo in hand.

*Alternative rejected:* resolve in `pullNarInfo` after parsing. That leaves the
shared helper returning a value its own doc comment describes as the NAR's
compression while it is not, and silently leaves the second caller
(`lookupPreferredUpstreamURL`, which gates on `originalURL.Compression == none` to
decide whether a re-fetch needs a preferred URL) reading the wrong value.

Precedence: an explicit URL extension wins over the header. That is the status
quo, it is what every conforming upstream does, and a genuine conflict between the
two is ambiguous with no evidence of any real upstream producing one. Resolution
applies only where the URL is silent.

**2. Emit a URL that carries the resolved compression.**

Whenever the resolved compression is non-`none` and the upstream URL carried no
extension, the narinfo ncps re-serves advertises
`nar/<narhash>.nar.<ext>`. This is what the opaque branch already does correctly
for cachix's `nar/<uuid>.nar.zst`; the fix makes the extension-less shapes behave
the same way.

This is load-bearing beyond cosmetics: it keeps `storeInDatabase`'s re-parse
correct without modifying it, and it is what makes the *client's* fetch correct —
a client asking for a bare `nar/<h>.nar` when the content is zstd is the user-
visible half of the bug.

*Alternative considered:* thread the resolved compression into `storeInDatabase`
and drop the re-parse entirely. That removes a fragile re-derivation, but it does
not fix the client-facing URL, so it would be an addition to Decision 2 rather
than a substitute. Deferred as a follow-up cleanup; the spec scenario asserting
narinfo/nar_file agreement guards the invariant in the meantime.

**3. `putNarInStore` and `narFileSize` are left untouched.**

`putNarInStore` branches on `narURL.Compression`; once that is `zstd` it takes the
store-as-received path and the double-compression disappears with no edit.

`narFileSize` seeding `NarSize` when a compressed narinfo omits `FileSize` looked
like a second defect, but `ensureNarFileRecord` (`cache.go:1841`) overwrites
`file_size` with the actual written byte count on conflict, keyed on the same
`(hash, compression, query)` tuple. The wrong value exists only in the window
before the NAR lands, on a row whose `bytes_stored_at` is still NULL and which is
therefore already treated as a placeholder. No requirement is warranted.

## Risks / Trade-offs

- **Transport-level zstd upstreams remain broken.** If an upstream applies zstd as
  a `Content-Encoding` over an uncompressed body, ncps strips it on ingest and is
  left holding a raw NAR, which this change will now confidently store under a
  `.nar.zst` key — internally consistent labels over lying bytes. → Mitigated by
  scope: that is a separate change against `upstream.GetNar`'s unconditional
  `Accept-Encoding: zstd`. The existing RED test in `pkg/cache` covers both
  variants, so the transport case stays visibly failing rather than silently
  forgotten.
- **The snix-castore shape changes source of truth.** Its compression previously
  came from a hard-coded `none`; it now comes from the header. snix-castore
  advertises `Compression: none`, so behaviour is unchanged in practice. → The
  existing spec scenario asserting `none` for that shape is retained verbatim as a
  regression guard.
- **Pre-existing drifted rows are not repaired.** Narinfos already stored with the
  desync keep serving from their stored URL until re-pulled. → Out of scope by
  design; `narinfo-compression-repair` (fsck) owns data repair.
- **A client holding a previously-served narinfo** has the bare `.nar` URL and
  `Compression: zstd`. After the fix, a re-pull yields the `.nar.zst` URL. →
  Narinfos are served from the database, so existing rows are unaffected until
  re-pulled; the serve-path compression fallback already tolerates a `none`
  request against a stored compressed whole file.

## Migration Plan

No database migration and no config change. The change is forward-only: new
narinfo pulls become self-consistent, existing rows are left as-is. Rollback is a
plain revert — nothing persisted by this change is unreadable by the prior
version, since the emitted URL/compression pair it writes is the same shape ncps
already writes for cachix-style opaque URLs.

## Open Questions

- Should `fsck --repair` be extended to heal already-drifted Attic-shaped rows
  (narinfo `zstd` ↔ nar_file `none` where the stored file is double-compressed)?
  Deferrable: it changes neither these specs, the approach, nor the task
  breakdown, and it belongs to `narinfo-compression-repair` if taken up.
