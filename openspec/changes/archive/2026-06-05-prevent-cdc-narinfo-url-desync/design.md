## Context

`pullNarInfo` fires the NAR download/chunking in a background goroutine, then synchronously normalizes + persists the narinfo. The old store-time condition `(isCDCEnabled() && !lazyChunking) || Compression==none` rewrote the URL to `none` and nulled FileHash for *all* eager-CDC narinfos — a prediction that the background chunking would make `none` true. The nar_file is created/finalized by the async path (`ensureNarFileRecord` then `SetTotalChunks`), so the two decisions are decoupled: a crash between the narinfo write and chunk completion strands `url=none` + an xz-only NAR.

The hash in the stranded URL is the **xz file hash** (not the NAR hash), proving the rewrite reused `narURL.Hash` (the compressed-file hash) when building the none URL — the bytes only ever existed at `.nar.xz`.

## Goals / Non-Goals

- **Goal**: never persist a narinfo advertising a compression the NAR is not (yet) stored under.
- **Non-Goal**: change the served representation of a chunked CDC NAR (still `none`) or the Harmonia none-path.

## Decisions

- **Remove the eager-CDC trigger from the store-time normalization.** The condition becomes simply `if narInfo.Compression == none`. Serve-time `maybeCDCNormalizeNarInfoURL` (already `HasNarInChunks`-gated) presents `none` once the NAR is genuinely chunked, so nothing is lost in the happy path; the desync window is closed.
- **Blast radius is small by construction.** With CDC off the eager trigger never fired; with lazy chunking it was already skipped. So the change only affects eager-CDC deployments, and only in the pre-chunk window.
- **Repair existing rows with a forward-only data migration** rather than code, reconstructing every field from the joined nar_file. The `NOT EXISTS` guard skips narinfos that also have a servable none/chunked backing.

## Risks / Trade-offs

- A CDC narinfo now advertises its truthful (xz) compression during the brief pre-chunk window instead of a predicted `none`. This is strictly more correct (the advertised bytes always exist) and is the mechanism of the fix. Two CDC contract tests that asserted the removed store-time `none` behavior are updated to assert the narinfo↔storage consistency invariant instead.
