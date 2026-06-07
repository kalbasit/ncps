## 1. Reproduce the race with a failing test (TDD red)

- [x] 1.1 Add a focused test in `pkg/cache/cdc_test.go` that constructs the failing state: a NAR whose chunks are committed and reassemblable, but whose whole file has been removed from `narStore` while `nar_file.total_chunks` is observed as `0` (the store-branch decision in `serveNarFromStorageViaPipe`). Assert `GetNar` returns the full original bytes.
- [x] 1.2 Run the new test and confirm it fails with `error fetching the nar from the store: not found` (verifies it captures the real defect, not a tautology).

## 2. Implement the chunk fallback (TDD green)

- [x] 2.1 In `serveNarFromStorageViaPipe` (`pkg/cache/cache.go`), when the store branch was chosen (`serveFromChunks == false`) and `getNarFromStore` returns `storage.ErrNotFound`, **and** CDC is enabled, **and** the request is uncompressed (`narURL.Compression == nar.CompressionTypeNone`), retry via `getNarFromChunks`.
- [x] 2.2 If the chunk fallback also misses, return the original `storage.ErrNotFound` (preserve genuine not-found semantics).
- [x] 2.3 Ensure the fallback does NOT engage for compressed requests (`Compression != none`) — those keep returning `ErrNotFound` so clients fall back upstream.
- [x] 2.4 Run the test from task 1 and confirm it now passes.

## 3. Harden the existing flaky test

- [x] 3.1 Update `testCDCMixedMode` so it deterministically asserts mixed-mode retrieval succeeds regardless of background-migration timing (no reliance on goroutine scheduling).
- [x] 3.2 Run `TestCDCBackends` repeatedly with the race detector (e.g. `go test -race -run TestCDCBackends -count=20 ./pkg/cache/`) and confirm zero failures.

## 4. Verify requirement coverage

- [x] 4.1 Confirm each scenario in `specs/cdc-chunking/spec.md` is covered by a test: migration-mid-serve fallback, mixed-mode retrieval, genuinely-absent not-found, and compressed-request not-found.
- [x] 4.2 Add/adjust assertions for any uncovered scenario.

## 5. Final verification

- [x] 5.1 Run `task fmt` and confirm it exits clean.
- [x] 5.2 Run `task lint` and confirm it exits clean.
- [x] 5.3 Run `task test` (and the Postgres integration cohort via `task test:auto` where the flake originally surfaced) and confirm all pass.
