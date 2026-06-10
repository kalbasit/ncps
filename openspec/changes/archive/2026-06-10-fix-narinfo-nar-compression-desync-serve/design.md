## Context

ncps stores each NAR keyed by `(hash, compression, query)` in `nar_files`, with the bytes on disk at `<hash>.nar[.<ext>]`. Uncompressed (`Compression: none`) NARs are by convention stored on disk as `.nar.zst` and decompressed transparently on read: `getNarFromStore` and `statNarInStore` (both in `pkg/cache/cache.go`) special-case a `none` request by consulting the `.nar.zst` file.

CDC normalizes a NAR's narinfo to advertise `Compression: none` / `URL: nar/<hash>.nar`. When CDC is later disabled (or for cross-codec upstreams), the same store path is re-downloaded and kept whole-file at the upstream's compression — commonly `.nar.xz` — while the narinfo still advertises `none`. The result is a **narinfo↔nar_file compression desync**: the client, trusting the narinfo, requests `/nar/<hash>.nar` (none), but the only stored representation is `.nar.xz`.

The serve path's `none` special-case only ever looks at `.nar.zst`, so `statNarInStore(none)` reports absent and `getNarFromStore(none)` cannot find the file. The request falls through to a re-download; when the upstream lacks the path or is unhealthy, the client sees `does not exist in binary cache`. The inverse drift (narinfo `xz`, stored uncompressed) yields `Lzma: No progress is possible`. Production DB inspection found ~259 drifted pairs; this change addresses the dominant direction (`none` advertised / compressed stored, ~200 pairs incl. all reported hashes).

## Goals / Non-Goals

**Goals:**
- Serve a `Compression: none` request from a locally-present whole-file NAR regardless of which supported compression it is stored as (zstd, xz, …), by transparent decompression.
- Preserve all existing serve behavior: direct serve of a matching-compression request, transparent-zstd pass-through for `Accept-Encoding: zstd`, CDC chunk serving, and correct `Content-Length`/compression labeling of the served stream.
- Stop unnecessary upstream re-downloads (and the resulting 404s) for desynced-but-locally-present NARs.

**Non-Goals:**
- The inverse direction — serving a compressed request (`xz`/`zstd`) when only an uncompressed or chunked representation is stored — which requires serve-time **re-compression** (a new compressor on the serve path). Deferred.
- A one-shot data repair of the existing ~259 drifted narinfo/nar_file pairs. Deferred.
- A write-path invariant keeping a narinfo's advertised compression equal to a servable representation. Deferred.

## Decisions

- **Generalize the existing transparent-decompress convention rather than introduce a new path.** A single helper `wholeFileServeCompressions()` returns the stored whole-file compressions that can satisfy a `none` request, in preference order `[zstd, xz]`. zstd stays first to preserve the canonical-encoding fast path; xz is added to cover the desync. Both `statNarInStore` (existence) and `getNarFromStore` (serve) iterate this list. *Alternative considered:* querying `nar_files` for any compression of the hash and statting that — rejected as more DB coupling for no benefit; the storage stat is the source of truth for what can actually be served.
- **Decompress based on the compression actually read from disk (`storedComp`), not the requested one.** `getNarFromStore` tracks `storedComp` separately from `narURL.Compression`; when they differ it wraps the reader in `nar.DecompressReader(ctx, r, storedComp)` (which already supports xz/zstd/bzip2/lz4/br/lzip) and reports size `-1`. `narURL.Compression` is left as `none` so the response is correctly labeled uncompressed. *Alternative:* mutating `narURL.Compression` to the stored value — rejected; it would mislabel the decompressed stream.
- **Transparent-zstd pass-through is gated on the stored compression being zstd.** Only `storedComp == zstd && TransparentZstd` streams stored bytes as-is; an xz-stored NAR requested with `Accept-Encoding: zstd` is decompressed (transparent-zstd disabled) rather than mislabeled.
- **LRU touch follows the served bytes.** When the served-representation row (`none`) is absent but the NAR was served by decompressing a stored compressed whole file, touch the stored compression's row instead of synthesizing a spurious `none` row. Healing (`needsDBRecord`) only triggers when neither row exists (a true orphan), matching prior behavior.

## Risks / Trade-offs

- **Extra storage stat per `none` request** (one additional `StatNar`/`HasNar` for xz when zstd is absent) → negligible: a metadata stat on the cold path, bounded by the 2-entry preference list.
- **Decompressing on serve costs CPU and yields unknown size (`-1`, chunked transfer)** → this already happens for the zstd `none` path; xz is the same shape. Net win versus a full upstream re-download.
- **Inverse-direction failures remain** (narinfo `xz`/`zstd`, stored uncompressed/chunked) → explicitly out of scope and tracked in #1392; this change does not regress them.

## Migration Plan

Behavior-only change in `pkg/cache`. No schema, migration, config, or API changes. Deploy by rolling the image; rollback is a plain revert. No data migration required (desynced rows are served correctly without modification).

## Open Questions

- None blocking. The deferred follow-ups (re-compression for the inverse direction, data repair, write-path invariant) are tracked in #1392 and can be scoped as separate changes.
