## 1. Reproduce (TDD red)

- [x] 1.1 Add a `pkg/cache` regression test: CDC enabled + lazy chunking, long delete-delay; `PutNar` an xz whole-file NAR + seed its narinfo; `MigrateNarToChunks` to create the coexisting chunked `none` row; assert the xz whole file is still present
- [x] 1.2 In the test, `GetNar` the xz URL and assert (currently FAILS): returned `Compression == xz`, and the streamed bytes equal the xz file (decompress to `NarSize`) — not relabeled `none` with a short `Content-Length`
- [x] 1.3 Run the test and confirm it fails for the documented reason (compression returned as `none`)

## 2. Fix (TDD green)

- [x] 2.1 In `getNarFromStore` remove the incorrect `narURL.Compression = nr.Compression` overwrite (`cache.go:3441`); ensure the returned compression reflects the file actually served (requested compression, after transparent zstd→none decompression)
- [x] 2.2 Confirm the `last_accessed_at` touch still targets an existing nar_file row for the served representation; adjust the WHERE/record selection if needed
- [x] 2.3 Run the new test (green) and the full `pkg/cache` CDC suite (no regressions)

## 3. Secondary hardening (only if needed)

- [x] 3.1 Assessed: the primary fix (serve path authoritative about compression) resolves the deterministic failure; no separate mid-serve deletion TOCTOU was reproducible, so no extra lock added (kept minimal)
- [x] 3.2 Primary fix suffices; change kept minimal (no secondary lock)

## 4. Verify and finalize

- [x] 4.1 `task fmt` (0 changed), `task lint` (0 issues), `task test` (full unit suite green incl. the new regression test) all exit zero
- [x] 4.2 Re-ran the e2e driver: Phase 2 (lazy) now PASSES, and the whole lifecycle (Phases 0-4 + upload presence) flows through. The previously failing libunistring serve is fixed.
- [x] 4.3 `openspec validate` passed; delta synced into openspec/specs/cdc-chunking; archiving now.
