## Context

The recovery sweep calls `gcOrSkipBackingLessNarFile` (`pkg/cache/cache.go`) for a `nar_file` whose whole-file bytes are gone and which migration cannot help. Current logic:

1. Query narinfos linked via `narinfo_nar_files` to this `nar_file`.
2. If **zero** linked narinfos → delete the `nar_file` (clean GC). Correct.
3. If **≥1** linked narinfo and **every** one is `narInfoGenuinelyAbsentUpstream` (confirmed gone from every healthy upstream; conservative — any Present/Unknown/no-upstreams aborts) → delete the `nar_file`.
4. Otherwise → skip; leave for on-demand `GetNar` recovery.

In step 3 the Ent on-delete cascade removes the `narinfo_nar_files` link rows but **not** the parent narinfos. Those narinfos survive with no link — dangling. They were already served as HTTP 200 and cached by Nix clients, so a later `nix build` fetches the NAR directly and gets a 404. Prod shows 2,744 such rows.

The read-path guard (`narinfo-purge-serving`) and `fsck --repair` already exist; neither prevents creation here, and the read-path guard is bypassed entirely by a Nix client-cache hit.

## Goals / Non-Goals

**Goals:**
- The backing-less GC MUST NOT leave a narinfo it can no longer serve. When step 3 deletes the `nar_file`, it deletes the linked (genuinely-absent-upstream) narinfos in the same transaction.
- Preserve the conservative deletion gate: nothing is deleted unless every linked narinfo is confirmed absent from every healthy upstream.

**Non-Goals:**
- Serving a NAR whose hash exists nowhere (an evicted local NAR that a Nix client-cache hit requests by hash is unrecoverable — the fix is to not retain the advertising narinfo, not to synthesize bytes).
- Read-path purge behavior, upload PUT ordering, LRU size-eviction, and Nix client TTL config — all unchanged / out of scope.

## Decisions

**D1 — Delete the linked narinfos with the nar_file (not just the cascade link).**
In the step-3 branch, after the all-absent check passes, delete the narinfos collected in `nis` and the `nar_file` atomically (single Ent transaction). Rationale: a narinfo that is unservable locally AND confirmed absent on every upstream is unrecoverable dead metadata; the only way it can ever be answered is a 404-after-200. Alternative considered: keep the narinfo and rely on `fsck` — rejected: fsck is a manual, periodic backstop, and the window between GC and fsck is exactly when Nix caches the 200 and the user's build breaks.

**D2 — Keep the all-absent gate as the safety boundary.**
Deletion of narinfos stays gated behind `narInfoGenuinelyAbsentUpstream` for *every* linked narinfo (existing behavior). A single Present/Unknown upstream, or zero healthy upstreams, still aborts the whole GC and leaves everything intact for on-demand recovery. This keeps the change strictly additive to an already-conservative deletion and avoids racing a cross-replica `nix copy`. Alternative: delete only the narinfos that are individually absent while keeping the nar_file — rejected: the nar_file is only deleted when *all* are absent, so partial deletion never applies in this branch.

**D3 — Reuse the existing transaction/cascade; no schema or migration change.**
The `narinfo_nar_files` on-delete cascade already fires when the parent narinfo is deleted, so deleting the narinfos cleans their link rows too. The `nar_file` delete remains; ordering within the transaction is irrelevant because both cascades are covered. No Ent schema change, no new migration.

## Risks / Trade-offs

- [A still-reachable narinfo is deleted because an upstream was transiently misreported absent] → Mitigated by the existing conservative gate: `ExistenceUnknown` and no-healthy-upstreams both yield "not absent", aborting deletion. Only `ExistenceAbsent` from every healthy upstream proceeds — the same bar that already authorizes deleting the `nar_file`.
- [Cross-replica `nix copy` reference check races the deletion] → The gate requires the path be absent on every upstream, i.e. not a live, re-uploadable path; and this is the background sweep, not the read path. Risk is no higher than today's `nar_file` deletion in the same branch.
- [Deleting metadata is irreversible] → Acceptable: the path is provably gone everywhere; a future upload PUT re-creates both narinfo and nar_file from scratch.

## Migration Plan

- Pure code change behind TDD. Deploy normally. No DB migration.
- Historical 2,744 rows are remediated out-of-band by the operator running `fsck --repair` once (already specified), independent of this change.
- Rollback: revert the commit; behavior returns to leaving narinfos (no data-shape change to undo).

## Open Questions

- Should the GC emit a metric/log counter for narinfos deleted as genuinely-absent (observability for how often this fires)? Leaning yes — a debug/info log mirroring the existing `garbage-collected genuinely-absent placeholder nar_file` line, extended with the deleted-narinfo count.
