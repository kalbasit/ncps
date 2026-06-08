## Why

The new `dev-scripts/test-inflight-staging-contention-e2e.py` driver (change `test-harness-coverage-audit`) reproduced genuine cross-replica contention and proved in-flight NAR staging **never actually serves** on real, fast downloads. Two coupled defects:

1. **Producer races a deleted temp file.** `produceStagingParts` opens the holder's download temp file (`pkg/cache/inflight_staging.go:225`, `os.Open(ds.assetPath)`). When a waiter's staging request is observed only at download completion — the `<-ds.done` branch of `waitForStagingRequest` returns `true` — the temp file has already been moved to final storage, so the open fails with `no such file or directory` and the producer logs a WARN error instead of cleanly no-op'ing.
2. **Activation is too sluggish to engage.** `stagingActivationPollInterval = 1s` (`inflight_staging.go:22`): the holder only checks for waiters once per second. Any download finishing within ~1s never has staging engage mid-stream, so contended readers silently fall back to storage-polling. The feature is effectively dormant except for multi-second downloads, defeating its purpose (#660 / #1289).

## What Changes

- **Producer no-ops gracefully when the download already completed.** If the staging request is observed at/after completion (temp file gone, `ds.finalSize` set), `produceStagingParts` returns without error — the NAR is already in storage and waiters serve from it. No more spurious WARN; an ENOENT on the temp file is treated as "already committed", not a failure.
- **Make activation prompt.** Replace (or supplement) the 1s poll with a signal: when a waiter records a staging request, the holder is notified immediately so it begins staging while the download is still in-flight. This lets staging engage on realistic contended downloads rather than only multi-second ones.
- **Validate green via the existing harness.** With these fixes, `task test:inflight-staging-contention` (adequately-sized `--package`) shows staging activate and serve in the **download window** across both the `local` and `s3` backends. (The **chunking window** surfaced a *separate* CDC serve-route defect — the non-downloading replica 404s the NAR instead of serving from staging — which this fix neither causes nor addresses; it is deferred to its own change.)

## Capabilities

### Modified Capabilities
- `inflight-nar-staging`: the producer MUST treat a completed download / absent temp file as a clean no-op (not an error), and staging activation MUST engage promptly once a cross-pod waiter requests it, not only after a coarse poll interval.

## Impact

- **Code**: `pkg/cache/inflight_staging.go` (producer + activation wait), small `downloadState` signalling in `pkg/cache/cache.go`. TDD on `pkg/cache` unit tests + the e2e driver for the multi-process proof.
- **I/O / latency / memory**: no change to steady-state serving; staging engages slightly sooner under contention (the intended behavior). Eliminates spurious WARN log noise.

## Non-goals

- Changing the staging part size, retention, or the storage/part-object format.
- Fixing the **chunking-window CDC serve-route 404** (the non-downloading replica 404s the NAR under CDC) — surfaced during validation, distinct from the producer temp-race; deferred to its own change.
- Reworking the lazy/CDC chunking paths.

(Validation required two small e2e-harness adjustments on this branch: the driver's default `--package` was set to `nixpkgs#gcc-unwrapped` — large enough to reliably activate and present on the upstream — and an opt-in `NCPS_E2E_UPSTREAM_URL/KEY` override was added so a locally-realised package can be fetched deterministically.)
