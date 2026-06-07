## Why

In production (v0.10.0-rc10) 2,744 narinfos have no `narinfo_nar_files` link. ncps once served each as a signed HTTP 200; Nix cached them client-side. The backing NAR was later deleted, so a subsequent `nix build` requests the NAR directly (skipping any narinfo re-fetch) and gets HTTP 404 — `error: file 'nar/<hash>.nar' does not exist in binary cache`. The existing read-path resilience (`narinfo-purge-serving`) cannot help: it only fires when Nix re-requests the *narinfo*, which a client cache hit bypasses.

The manufacturing path is the background recovery GC. `gcOrSkipBackingLessNarFile` deletes a NAR whose bytes are gone when every linked narinfo is *genuinely absent from every healthy upstream*. The on-delete cascade removes only the `narinfo_nar_files` link rows — it leaves the narinfos behind, now dangling and unservable forever (the path exists nowhere: not locally, not upstream).

## What Changes

- When the backing-less-NAR GC deletes a `nar_file` because all of its linked narinfos are genuinely absent upstream, it MUST also delete those narinfos in the same transaction. A narinfo that is unservable locally and confirmed gone from every upstream is dead metadata; retaining it guarantees a future served-then-404.
- The existing 0-linked-narinfo GC branch is unchanged (already correct).
- No change to the read path, to upload ordering, or to LRU size-eviction. Those either already behave correctly or are out of scope (see Non-goals).
- `fsck --repair` remains the backstop for the 2,744 historical rows (already specified); the operator runs it once. The ingress streaming fix (separate, already shipped) closes the upload-reset origin of new dangling narinfos.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `nar-cache-miss-recovery`: the backing-less `nar_file` GC gains a requirement to delete the linked, genuinely-absent-upstream narinfos together with the `nar_file`, so the recovery sweep never leaves a dangling narinfo.

## Impact

- Code: `pkg/cache/cache.go` `gcOrSkipBackingLessNarFile` (and its recovery-sweep callers). Tests under `pkg/cache/recovery_gc_*_test.go`.
- Data: the genuinely-absent branch now deletes narinfos it previously orphaned. Bounded, transactional, and only for paths already proven gone everywhere.
- No schema change (link cascade already exists; this deletes the parent rows the cascade leaves behind). No migration required.

## Non-goals

- Healing a NAR GET whose hash exists nowhere (a Nix client-cache hit for an evicted local NAR cannot be served — only avoided by not retaining the narinfo).
- Reworking LRU size-based eviction or upload PUT ordering.
- Changing the deliberately non-destructive read-path purge guard.
- Client-side: Nix's `narinfo-cache-positive-ttl` is operator config, not ncps's concern.

## I/O, network, memory impact

- Net **reduction** in steady-state work: dead narinfos stop being advertised and stop being re-scanned each recovery sweep. One extra `DELETE ... WHERE id IN (...)` per GC'd backing-less row (rows that are already being deleted). No added network calls — the upstream-absence probes already run. Negligible memory.
