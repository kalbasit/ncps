## Why

Serving a pre-existing **whole-file xz NAR** returns the bytes labeled with the **wrong compression** once CDC is enabled, so `nix` fails with `NAR ... is incomplete` and `input compression not recognized`. This deterministically breaks the "enable CDC on a cache that already holds whole-file NARs" upgrade path — the new `dev-scripts/test-cdc-lifecycle-e2e.py` driver reproduces it every run (Phase 2 lazy, serving `libunistring-1.4.2`).

Root cause (verified): `pkg/cache/cache.go` `getNarFromStore()` opens and streams the raw xz whole-file bytes (`storeURL.Compression=xz`, no decompression), but then at `cache.go:3441` overwrites `narURL.Compression` with the compression of the record returned by `getNarFileFromDB()`, which is **CDC-first** (prefers the chunked `compression=none` row). The response therefore advertises `Compression:none` while the body is raw xz, and `Content-Length` is the xz file size (< `NarSize`). This is the NAR-serve side of the same "served compression ≠ served bytes" desync the narinfo side already fixed via `maybeCDCNormalizeNarInfoURL` (#1332).

## What Changes

- **Fix** `getNarFromStore()` so the whole-file serve path is authoritative about compression: it MUST report the compression of the file it actually opened/served (the requested `narURL.Compression`, accounting for transparent zstd→none decompression), NOT the CDC-first DB row's compression. Remove the incorrect overwrite at `cache.go:3441`.
- **Add** a regression test in `pkg/cache` (TDD): serve a whole-file xz NAR while CDC + lazy chunking is enabled and a chunked `none` row coexists; assert the served compression is `xz` and the served bytes total the xz file size / decompress to `NarSize`.
- Secondary hardening (only if the test shows it is needed): guard the whole-file bytes from lazy-cleanup deletion mid-serve.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `cdc-chunking`: add a requirement that `GetNar` MUST advertise the compression of the bytes it actually serves (the whole-file serve path must not relabel served bytes from a CDC-first DB lookup). Complements the existing "GetNarInfo MUST normalize compression" and "GetNar MUST 404 for compressed URL when only chunks exist" requirements.

## Impact

- **Code**: `pkg/cache/cache.go` (`getNarFromStore`), plus a new `pkg/cache` regression test. No DB schema change, no migration, no API change.
- **Behavior**: NARs stored before CDC was enabled serve correctly after enabling CDC (eager and lazy). Fixes a data-correctness/serving bug; no change for caches that never used whole-file NARs.
- **I/O / network / memory**: none — same I/O path; the fix only corrects the compression label (and `Content-Length`) on the existing whole-file read.

## Non-goals

- Not changing chunked-NAR reassembly behavior (covered by `chunked-nar-serving-integrity`).
- Not changing the lazy-recovery rechunking schedule or the migrate-chunks-to-nar drain.
- Not redesigning `getNarFileFromDB`'s CDC-first lookup (used for record selection); only the serve path's compression resolution is corrected.
