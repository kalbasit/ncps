## 1. Reproduce in a unit test (red)

- [x] 1.1 Add a failing `pkg/cache` internal test asserting `produceStagingParts` returns nil (no error, no WARN) when `ds.assetPath` points to a file that no longer exists (the download already completed and the post-completion cleanup removed the temp file). Model it on `inflight_staging_producer_internal_test.go`.

## 2. Producer graceful no-op (green for 1.1)

- [x] 2.1 In `produceStagingParts` (`pkg/cache/inflight_staging.go`), when `os.Open(ds.assetPath)` returns an `fs.ErrNotExist`, return nil — the temp file is only ever removed by the post-completion cleanup goroutine (`cache.go:3143`), so a not-exist error unambiguously means the download already completed and its NAR is in shared storage. Any other open error stays a real error.
- [x] 2.2 Emit a single debug line for the no-op (download already completed before staging began), removing the spurious WARN for this case.

## 3. Prompt activation (shorten the holder poll)

- [x] 3.1 Lower `stagingActivationPollInterval` (`pkg/cache/inflight_staging.go`) from `1s` to `200ms`, matching the waiter's `pollInterval` (`cache.go:6517`) so the holder observes a cross-pod staging request while the download is still in flight. Update the now-stale "coarse on purpose" comment. (No per-hash local signal — staging requests are cross-pod-only, so there is nothing in-process to signal; see design D2.)
- [x] 3.2 Confirm backfill-from-zero is unchanged: activation landing mid-download still backfills the already-downloaded prefix from offset zero (no code change expected; verify via the existing producer/late-waiter tests).

## 4. Unit verification

- [x] 4.1 `task test` green (race detector), including the new tests and the existing `inflight_staging_*` suite.
- [x] 4.2 `task fmt` and `task lint` exit zero.

## 5. Multi-process acceptance (the bug's real proof)

- [x] 5.1 Ran `task test:inflight-staging-contention -- --storage local --window download` (gcc-unwrapped, ~277MB NAR, 2 replicas, 8 clients). PASS: staging ACTIVATED (4 activation log lines on the non-holder replica), all readers 200, byte-identical to canonical `nix-store --dump`, and `producer_error=[]` — the exact pre-fix failure (producer ENOENT WARN) is gone. This is the download-window (#660) proof.
- [x] 5.2 `s3-download`: PASS (staging activated, same as local) — download window validated on both backends. Chunking window (`local-chunking`, `s3-chunking`) DESCOPED to a separate change: it fails on a distinct CDC serve-route bug (non-downloading replica 404s the `.nar.xz`, 4/8 readers, no producer error/no staging serve), which this fix neither causes nor addresses. Documented in [[project_cdc_window_nonholder_404]].
- [x] 5.3 Evidence: `.e2e-results/inflight-staging/20260608-144032/` (local-download PASS, `activated_on:[8502]`, package `nixpkgs#gcc-unwrapped`) and `.../20260608-144129/summary.json` (matrix: local-download PASS, s3-download PASS, *-chunking FAIL on the separate CDC bug). Default `--package` set to `nixpkgs#gcc-unwrapped` (large + on cache.nixos.org + reliably activates; `nixpkgs#go` was local-store-only → upstream 404).
