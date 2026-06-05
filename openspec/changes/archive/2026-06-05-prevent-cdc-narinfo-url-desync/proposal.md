## Why

Under eager CDC, `pullNarInfo` rewrote the persisted narinfo URL to `nar/<hash>.nar` (Compression none) and nulled FileHash **synchronously**, *predicting* that the asynchronous chunking would complete. The chunked `nar_file` is first written whole-file under its real compression (e.g. xz, `total_chunks=0`) and only flipped to none/chunked later. If chunking never completes (crash/restart in that window), the narinfo is left permanently advertising a `none` URL while the NAR is stored xz-only at `/nar/<hash>.nar.xz` — so a client GET of `/nar/<hash>.nar` 404s and a `nix copy --to .../upload` reference check aborts with "the reference does not exist". 127 production narinfos were stranded this way.

## What Changes

- Persist the narinfo **truthfully**: only normalize to `none` for genuinely `Compression:none` upstreams (stored zstd, served none transparently — the Harmonia path, unchanged). **Do not** predict `none` for CDC. Serve-time `maybeCDCNormalizeNarInfoURL` (gated on `HasNarInChunks`) remains the sole mechanism that presents `url=none`, and only once the NAR is genuinely chunked — so steady-state serving is unchanged.
- Repair the 127 already-stranded narinfos via a forward-only per-dialect data migration: restore the xz URL, compression, FileHash (`'sha256:'||nar_file.hash`) and FileSize from the joined `nar_file`; exclude narinfos that also have a servable none/chunked backing.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `cdc-chunking`: narinfo URL normalization to `none` is no longer performed at store time for CDC; the persisted narinfo reflects the NAR's actual stored compression until the NAR is genuinely chunked, at which point serve-time normalization presents `none`.

## Impact

- `pkg/cache/cache.go`: `pullNarInfo` store-time normalization condition.
- `migrations/{sqlite,postgres,mysql}/*_repair_url_none_xz_narinfos.sql`: one-time data repair (idempotent; no-op on clean databases).
- Behavior change is confined to the pre-chunk window (a CDC narinfo now advertises its truthful compression until chunked instead of a predicted `none`); steady-state serving and the Harmonia none-path are unchanged.
