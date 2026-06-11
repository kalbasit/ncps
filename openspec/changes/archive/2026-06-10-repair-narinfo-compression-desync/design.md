## Context

`narinfos` advertises `url`/`compression`/`file_hash`/`file_size`; `nar_files` stores the physical NAR keyed by `(hash, compression, query)`. CDC normalizes a narinfo to `Compression: none` (`maybeCDCNormalizeNarInfoURL`), but drift leaves some narinfos advertising `xz` while the NAR is stored as none/zstd/chunks. The serve path can now produce `none` (decompress any) and `zstd` (recompress), but not `xz` (no compressor), so an `xz`-advertised/non-xz-stored narinfo still fails. fsck (`pkg/ncps/fsck.go`) already has narinfo repair helpers (e.g. `relinkNarInfoToBackingNarFile`) invoked from `repairFsckIssues` under `--repair`.

## Goals / Non-Goals

**Goals:**
- One-shot, idempotent repair of pre-existing `xz`-advertised/non-xz-stored narinfos: rewrite to the servable `none` form so they serve via transparent decompression.
- Leave healthy narinfos untouched; safe to run repeatedly.

**Non-Goals:**
- Serve-time changes (covered by #1393 / serve-zstd-from-chunks).
- Preventing new drift (the write-path-invariant change).
- Repairing narinfos with no backing NAR at all (the existing orphan-narinfo fsck path handles those).
- Adding an xz compressor.

## Decisions

- **Rewrite to `none`, not to the stored compression.** `none` is universally servable (the serve path decompresses any stored whole-file/chunk representation), so rewriting to `nar/<nar_hash>.nar` + `Compression: none` + cleared `FileHash`/`FileSize` is correct regardless of whether the backing is none, zstd, or chunks. This reuses the exact shape `maybeCDCNormalizeNarInfoURL` produces, so the serve path already handles it. *Alternative:* rewrite to match the stored compression — rejected: more cases, and zstd/none both serve fine as `none`.
- **Target only non-producible advertised compressions with a differing backing.** A narinfo is repaired iff its advertised compression is not producible (not `none`/`zstd`) and no `nar_file` exists in that advertised compression. This precisely captures `xz`-advertised/non-xz-stored and avoids touching healthy or already-servable rows. *Alternative:* repair every drift — rejected: serve-side already handles none/zstd drift; rewriting them is churn.
- **Live in fsck `--repair`, operate on `dbClient`.** Mirrors `relinkNarInfoToBackingNarFile`; bulk-queries the affected narinfos rather than threading a pre-collected suspect list, keeping the change self-contained.

## Risks / Trade-offs

- **Rewriting a narinfo to `none` when the backing bytes are absent would still 404** → guarded: only rewrite when a backing `nar_file` exists under the NAR hash; rows with no backing are left to the existing orphan path.
- **Signature/`FileHash` semantics** → `none` narinfos carry no `FileHash`/`FileSize` by convention (enforced elsewhere); clearing them matches that and the NAR-hash-based signature remains valid.
- **Idempotency** → after rewrite the narinfo advertises `none`, which no longer matches the repair predicate, so re-running is a no-op.

## Migration Plan

Ships as fsck repair logic; operators run `ncps fsck --repair` once to reconcile existing rows. No DB migration. Rollback: revert the binary; rewritten narinfos remain valid (they serve correctly).

## Open Questions

- None blocking.
