## Why

`TestCDCBackends/.../Mixed_Mode` (`pkg/cache/cdc_test.go:316`) fails intermittently in CI
with `error fetching the nar from the store: not found` and the failure rate is rising. The
flake exposes a real **time-of-check/time-of-use race** in the NAR read path, not merely a
brittle test: when a whole-file NAR is served while CDC is enabled, `GetNar` spawns a
**background** NAR→chunks migration that deletes the whole file from the store, then
*synchronously* serves from that same store. If the background delete wins the race before
`getNarFromStore` opens the file — while `nar_file.total_chunks` is still `0`, so the serve
path chose the store branch — the read 404s. In production this surfaces as spurious
`not found` errors on freshly-migrated NARs.

## What Changes

- **Fix the read-path race** so a `GetNar` that observed the whole file in the store never
  returns `not found` when a concurrent background migration removed it. The serve path
  (`serveNarFromStorageViaPipe` / `getNarFromStore`) MUST fall back to serving from chunks
  when the whole-file read misses while CDC is enabled, instead of surfacing
  `storage.ErrNotFound` to the caller.
- **Make the test deterministic** by asserting the corrected behavior (mixed-mode retrieval
  of a blob stored before CDC was enabled always succeeds), covering the race window rather
  than depending on background-goroutine timing.
- No change to the on-disk format, the migration semantics, or the chunk store.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `cdc-chunking`: Add a requirement that the whole-file serve path is resilient to a
  concurrent background NAR→chunks migration — a store read that misses (because migration
  deleted the whole file) MUST fall back to chunk reassembly rather than returning
  `not found`, so serving a NAR observed as present never races to a 404.

## Impact

- **Code**: `pkg/cache/cache.go` — `GetNar` / `serveNarFromStorageViaPipe` / `getNarFromStore`
  fallback logic; `pkg/cache/cdc_test.go` — `testCDCMixedMode` assertions.
- **APIs**: none. HTTP/storage interfaces unchanged.
- **I/O**: in the rare race, one extra store-read miss before falling back to chunks (a few
  metadata lookups). No additional network calls; memory unchanged (chunk reassembly already
  streams). Steady-state latency unaffected.

## Non-goals

- Not redesigning the background NAR→chunks migration or the "migrate chunks" topology
  (tracked separately as `migrate-chunks-to-nar`).
- Not changing when migration is triggered, drain mode, or CDC enable/disable semantics.
- Not addressing other flaky tests or unrelated backends; scope is this one race and its
  regression coverage.
