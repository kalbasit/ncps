## 1. Reproduce in a unit test (red)

- [x] 1.1 Added `pkg/cache/has_finished_nar_internal_test.go`: `TestHasFinishedNarExcludesActivelyChunking` asserts the crux split — an actively-chunking NAR (`total_chunks==0`, chunker live) is `isServable==true` but `hasFinishedNar==false`, so the loser no longer mis-routes it as a finished served-by-peer asset. Plus `TestHasFinishedNarCountsFullyChunked`. Both pass.
- [~] 1.2 The full coordinate-loop staging-serve assertion is covered by the e2e (5.x), not a unit test (the 4-state race needs real multi-replica infra).

## 2. Split the served-by-peer decision by state (green)

- [x] 2.1 In `pollForDownloadOrTakeOver` (`pkg/cache/cache.go:6543+`), the cyclic priority (see design D1) means a reorder is insufficient. Split the per-tick decision into four states: (1) dead holder (lock acquirable) → takeover+restart, stays FIRST (D5); (2) **finished** (`HasNarInStore` OR `total_chunks>0`) → served-by-peer completed `ds`; (3) actively-chunking + `stagingActive` + `stagingServeReady` → `ds.stagingServe`; (4) actively-chunking + no staging → completed `ds` (progressive chunk streaming, current behavior). The key change: the served-by-peer return must fire only for the *finished* state, not for an actively-chunking NAR (which `isServable` currently conflates).
- [x] 2.2 Introduce a `hasFinishedAsset` predicate (finished-only; NOT actively-chunking) for the served-by-peer return, distinct from the existing `isServable` (which stays "can serve now" for progressive routing in state 4). Do NOT change `isServable` globally.
- [x] 2.3 Gate state (3) on `stagingActive` so feature-off behavior is byte-for-byte unchanged (state 4 progressive streaming preserved). Update the surrounding comments / D5 note to reflect the state split.

## 3. Confirm CDC staging actually engages end-to-end

- [!] 3.1 STOP CONDITION HIT. The chunking-window e2e (after the coordination fix) still 404s with ZERO staging activation: the CDC holder configures staging at startup but produces NO parts (`stageInflightNar` at cache.go:3019-3032 runs for CDC, but no `advanceStagingParts`/activation line appears). So in-flight staging does not produce parts in the CDC download path — the loser reaches the staging check (C) but `stagingServeReady` is always nil, falling through to chunk-serve → 404. This is a deeper, SEPARATE piece (wiring the staging producer into the CDC decompress→chunk download path, and/or its interaction with the producer's ENOENT no-op and `waitForStagingRequest` timing) and is escalated rather than forced here. See [[project_cdc_staging_producer_not_wired]].

## 4. Unit verification

- [x] 4.1 `task test` green (race), with emphasis on the coordination/CDC/takeover/recovery suites (`coordination_internal_test.go`, `cdc_*`, `*takeover*`, `recovery_gc*`) — confirm no regression to progressive streaming or takeover.
- [x] 4.2 `task fmt` and `task lint` exit zero.

## 5. Multi-process acceptance — DEFERRED to the Layer-2 change

The chunking-window e2e cannot go green from Layer 1 alone: the CDC holder produces no staging parts (task 3.1), so `stagingServeReady` is always nil and the contended loser still falls through to the chunk-serve 404. The chunking-window e2e green is owned by the Layer-2 change (wire the CDC staging producer). Layer 1's verification is the `hasFinishedNar` unit test + the full `-race` suite (no regression), confirmed in §4.

- [x] 5.1 (Layer-1 scope) Confirmed via the e2e that the coordination fix changes routing (loser no longer mis-routes an actively-chunking NAR as served-by-peer); end-to-end serve still blocked by the missing producer (Layer 2). Evidence: `.e2e-results/inflight-staging/20260608-153309/` (chunking still 404s, 0 activation — Layer-2 gap).
- [~] 5.2 Chunking-window green (local + s3) → Layer-2 change.
- [~] 5.3 Full-matrix green → Layer-2 change.
