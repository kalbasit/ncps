## 1. Reproduce (RED)

- [x] 1.1 Add a deterministic regression test in `pkg/cache/*_internal_test.go` that drives the stale-`hasInStore` race: whole file present + chunk store nil + `hasInStore=false`, asserting the current code fails with `chunk store not initialized, cannot serve NAR from chunks`.
- [x] 1.2 Confirm the new test reproduces the exact CI error (RED) and matches the failing `RunLRUCleanupInconsistentNarInfoState` symptom.

## 2. Fix the serve path (GREEN)

- [x] 2.1 In `serveNarFromStorageViaPipe` (`pkg/cache/cache.go`), gate the `serveFromChunks := !hasInStore` chunk route on `c.isChunkStoreAvailable()` so an uncompressed request is never routed to `getNarFromChunks` when no chunk store is configured; resolve against the whole-file store instead.
- [x] 2.2 Preserve `storage.ErrNotFound` semantics: a genuinely-absent whole file with no chunk store still returns `ErrNotFound` (cache-miss recovery / upload-only contract), and the #1324 store→chunks fallback remains unchanged when a chunk store exists.
- [x] 2.3 Add a code comment explaining the inverse-TOCTOU rationale, referencing #1324 and the stale `hasInStore` flag.
- [x] 2.4 Verify the RED test from 1.1 now passes (GREEN).

## 3. Verify against spec scenarios

- [x] 3.1 Cover "whole-file lands between time-of-check and serve, no chunk store" → serves successfully, no chunk-store error.
- [x] 3.2 Cover "uncompressed serve never routes to chunks when no chunk store exists".
- [x] 3.3 Cover "genuinely absent NAR with no chunk store recovers via re-download" (non-upload-only). _Covered by the now-passing `RunLRUCleanupInconsistentNarInfoState` integration test (GetNar re-download path); no duplicate unit test added._
- [x] 3.4 Cover "genuinely absent NAR in upload-only mode returns `storage.ErrNotFound`". _GetNar returns `ErrNotFound` in the `IsUploadOnly` branch before the serve path; unaffected by this change and covered by existing upload-only tests._
- [x] 3.5 Confirm no regression to chunk-store-present behavior (existing `TestCDCBackends` and `cdc_migration_race_internal_test.go` still pass).

## 4. Validate

- [x] 4.1 Run the targeted test under `-race` repeatedly to confirm the flake is gone.
- [x] 4.2 Run `task fmt`, `task lint` (0 issues), and `task test` — all exit 0.
- [x] 4.3 Run the integration suite for the postgres cohort (`task test:auto`) to confirm the original CI failure is resolved. _Full `task test:auto` passed (exit 0); `pkg/cache` ran 38s against real backends with 0 failures._
- [x] 4.4 Update the `cdc-chunking` spec via `/opsx:sync` (or archive flow) once implementation is verified. _Synced at archive: the ADDED requirement + 4 scenarios appended to `openspec/specs/cdc-chunking/spec.md`._
