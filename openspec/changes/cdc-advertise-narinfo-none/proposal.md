## Why

Under eager CDC the in-flight NAR exists only as **decompressed** bytes (temp, staging parts, and chunks are all `Compression: none`), and ncps has **no NAR compressor**. While chunking is in progress the narinfo still advertises the upstream compression (e.g. `xz`), so clients request `.nar.xz` — which cannot be served correctly. Today that request either 404s → upstream fallback (wasteful) or serves decompressed bytes mislabeled as `xz` (silent corruption). The in-flight staging feature (now merged) makes `none` bytes servable throughout the pull **and** chunk window, removing the historical blocker (cache.go:4082) that made predictive `none` advertisement unsafe.

## What Changes

- Advertise narinfo `Compression: none` / `.nar` URL **consistently and predictively** for eager-CDC NARs — at store time and at `GetNarInfo` serve time — *before* any chunk exists, so clients always request `.nar` and receive the same decompressed bytes from temp / staging / progressive chunks / finished chunks.
- Scope strictly to **eager** CDC (no whole-file in store). Lazy CDC keeps the whole `xz` file and continues serving `.nar.xz` correctly — it is **not** normalized.
- Guard the `nix copy` **upload-only** path so predictive `none` cannot desync narinfo↔storage — the exact area where the prior reference-404 / phantom-purge bugs lived.
- Net effect: the `.nar.xz` request during the chunking window stops happening at its source, eliminating both the 404→fallback and the corruption.

No external API break: clients transparently fetch `.nar` instead of `.nar.xz`.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `cdc-chunking`: broaden "GetNarInfo MUST normalize compression in-memory" from *already-chunked* (`HasNarInChunks`) to *any eager-CDC NAR* (predictive), and add equivalent store-time normalization so the cold/triggering client also requests `.nar`.
- `inflight-nar-staging`: state that uncompressed staging bytes are the canonical source satisfying the predictively-normalized `.nar` request across the pull+chunk window.

## Impact

- **Code**: `cache.go` store-time narinfo normalization switch (~4082-4108), `maybeCDCNormalizeNarInfoURL` (~8242), `GetNarInfo` serve path (~3819), and a guard on the upload-only path. No new flags, schema, or migrations.
- **Network**: clients download uncompressed `.nar` (larger on the wire than `.nar.xz`) during the eager-CDC window — but this only moves the existing post-chunk `none` advertisement earlier, and it removes redundant upstream `.nar.xz` re-fetches. Net upstream traffic ↓.
- **I/O / memory**: neutral. No new buffering, no re-compression (impossible by design), no extra DB writes beyond the existing async narinfo update.

## Non-goals

- **No** NAR (re-)compressor — ncps still cannot synthesize `.nar.xz` from chunks; this change makes that unnecessary, not possible.
- **No** change to lazy CDC serving (whole `xz` stays, served as `.nar.xz`).
- **No** chunk storage-format change, flags, schema, or migrations.
- **No** redesign of the contention/lifecycle e2e harness here (separate follow-up).
