## 1. Site 1 — route uncompressed actively-chunking cross-pod reads to coordination (TDD)

- [x] 1.1 RED→GREEN: `TestShouldCoordinateInflightUncompressedRequiresStaging` — uncompressed coordinates only when staging enabled
- [x] 1.2 RED→GREEN: same test asserts staging-disabled → no coordination (progressive preserved); `TestShouldCoordinateInflightCompressedAlways` guards compressed
- [x] 1.3 GREEN: `cache.go:1320` now `!finished && c.shouldCoordinateInflight(narURL.Compression)`; added `shouldCoordinateInflight` predicate

## 2. Site 2 — poll-loop state (D) waits for staging parts before progressive (TDD)

- [x] 2.1 RED→GREEN: `TestPollDStateWaitsForStagingPartsBeforeProgressive` — (D) waits for staging parts (staged 300ms after polling) and serves from staging
- [x] 2.2 RED→GREEN: `TestPollDStateFallsBackToProgressiveWhenStagingStalls` — bounded wait → progressive fallback, no hang
- [x] 2.3 GREEN: `TestPollDStateProgressiveWhenStagingDisabled` — `!stagingActive` unchanged (immediate progressive)
- [x] 2.4 GREEN: `cache.go:6815` gates progressive on staging-wait exhaustion (`maxStagingWaitTicks=15`); `stagingActive` keeps polling

## 3. Verify ncps unit + integration

- [x] 3.1 `go test -race ./pkg/cache/` green (33.8s) + full `task test` green — no regression
- [x] 3.2 Covered by the e2e (real 2-replica postgres+s3+redis multi-process run), stronger than unit integration

## 4. Flip the e2e assertion + harness tests

- [x] 4.1 `_run_chunking_window` reverted to assert staging activation (removed chunked-by-one); kept the in-flight gate; added bounded clean-restart retry (addresses CodeRabbit #1386 review)
- [x] 4.2 `tests/test_staging_contention.py` green (43/43); kept `_inflight_state`/`_await_inflight`/`_hash_from_nar_url`; applied Gemini `hash`→`nar_hash` rename
- [x] 4.3 `nix/e2e-tests/README.md` reverted to staging-activation for both windows; `unified-e2e-harness` main spec corrected via /opsx:sync below

## 5. End-to-end validation

- [x] 5.1 `task test:e2e ... staging-contention` — BOTH windows PASS with staging activation; chunking-window race 143s→4.5s (staging replaced progressive)
- [x] 5.2 Determinism confirmed: runs #6 and #7 both PASS both windows with staging activation [1] (~4.5s each)

## 6. CHANGELOG + finalize

- [x] 6.1 CHANGELOG entry added: eager-CDC uncompressed cross-pod reads now actually engage staging (two-site read-path fix)
- [x] 6.2 `task fmt` clean; `task lint` clean (0 issues) after `golangci-lint cache clean` + removing `var/ncps/nix-tmp`; `task lint:fix` resolved 4 wsl/nlreturn nits in the test
- [x] 6.3 `openspec validate --all` → 41 passed, 0 failed (incl. corrected unified-e2e-harness + inflight-nar-staging main specs)
- [ ] 6.4 Cannibalize PR #1386: same branch, update PR title/description once landed
