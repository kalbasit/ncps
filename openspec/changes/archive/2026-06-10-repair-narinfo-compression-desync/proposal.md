## Why

After the serve-side fixes (#1393 and serve-zstd-from-chunks), ncps tolerates most narinfo↔nar_file compression drift at serve time by transcoding. The one case it still cannot serve is a narinfo advertising a compression ncps has no compressor for — in practice `xz` — while the NAR is stored under a different compression (none/zstd/chunks). DB inspection of the live cache found ~59 such pairs (`xz|none` 53, `xz|zstd` 6); each 404s or yields `Lzma: No progress is possible`. These are pre-existing drifted rows that the serve fixes cannot heal at request time, so they need a one-shot data repair.

## What Changes

- `ncps fsck --repair` SHALL repair narinfos that advertise a non-producible compression (`xz`) whose backing NAR is stored under a different compression, by rewriting the narinfo to the servable uncompressed form: `URL: nar/<nar_hash>.nar`, `Compression: none`, and clearing `FileHash`/`FileSize` (matching the CDC narinfo normalization the serve path already expects). The uncompressed NAR is then served by transparent decompression of the stored bytes (#1393).
- A healthy narinfo (advertised compression matches a stored representation, or is directly producible) MUST NOT be modified.
- The repair is idempotent and reports how many narinfos were rewritten.

## Capabilities

### New Capabilities
- `narinfo-compression-repair`: A one-shot fsck repair that rewrites narinfos advertising a non-producible compression (no matching stored NAR) to the servable `none` form, reconciling pre-existing compression drift.

### Modified Capabilities
<!-- none -->

## Impact

- `pkg/ncps/fsck.go`: new `repairNarInfoCompressionDesync` bulk repair, invoked from `repairFsckIssues`; a count in the fsck summary.
- New unit test in `pkg/ncps`.
- Repair runs only under `--repair`; read-only fsck is unchanged. No schema/migration/API change.
