## 1. Reproduce in a unit test (red)

- [~] 1.1 A GetNar unit test is NOT viable: `GetNar` calls `serveNarFromStorageViaPipe` at TWO points (the 1232 short-circuit and the post-coordination serve ~1330), and BOTH 404 on compressed-from-chunks. In a unit context (local locker, no upstream, no real cross-pod staging) the gated fall-through just hits the 1330 chunk-404, so the gate's effect is externally indistinguishable. The gate's effect is only observable in the multi-process e2e (redis contention → the loser reaches `pollForDownloadOrTakeOver`, which can return `stagingServe`). The predicate the gate relies on (`hasFinishedNar` excludes actively-chunking) is unit-tested in the prior change. Verification is the e2e (§4) + the `-race` suite.

## 2. Gate the GetNar short-circuit (green)

- [x] 2.1 In `GetNar` (`pkg/cache/cache.go` ~1216-1232), after the existing `hasNar` computation (including the `hasActiveLocalJob` re-eval), add: if `hasNar && narURL.Compression != nar.CompressionTypeNone && !c.hasFinishedNar(ctx, narURL)` then set `hasNar = false` (actively-chunking + compressed request → coordinate/stage instead of chunk-serving → 404). Add a clear comment referencing #1289 and the staging transcode path.
- [x] 2.2 Confirm the uncompressed path is untouched (condition requires `Compression != none`) and finished NARs are untouched (`hasFinishedNar` true → condition false).

## 3. Unit verification

- [x] 3.1 `task test` green (race), with emphasis on `cdc_*`, progressive-chunk, coordination, and `GetNar`/serve tests — confirm no regression to uncompressed progressive serving or the holder path.
- [x] 3.2 `task fmt` and `task lint` exit zero.

## 4. Multi-process acceptance (the bug's real proof)

- [x] 4.1 VERIFIED: with the GetNar gate, the loser now REACHES coordination — its log shows 2 lock-acquisition lines (vs 0 before the gate), so it falls through and records a staging request. This change's deliverable (loser coordinates instead of short-circuiting to chunk-404) is confirmed.
- [!] 4.2 STOP CONDITION HIT (as pre-authorized). The loser now coordinates but staging STILL yields nothing (0 activation, 4/8 readers 404): the CDC holder produces no staging parts despite a recorded request. This is the separate downstream producer defect — now finally investigable (the loser records a request, which it never did before this gate). Escalated to its own change. See [[project_cdc_staging_producer_not_wired]].
- [~] 4.3 If green: run `--window chunking --storage s3` and the full matrix (`--storage both --window both`); capture `.e2e-results/inflight-staging/<ts>/summary.json` as evidence.
