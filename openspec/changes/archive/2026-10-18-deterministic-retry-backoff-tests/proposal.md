## Why

ncps v0.10.0 fails to build in nixpkgs on aarch64-linux (NixOS/nixpkgs#574569, reproducible on
Hydra build 349120500) because `TestDoRequest_NoBackoffOnFinalAttempt` asserts a **wall-clock**
upper bound: three attempts with a 60ms base backoff must finish in `< 5*base` (300ms). On a loaded
Hydra builder the run took 752ms. The production behaviour is correct (no backoff is applied after
the final attempt). The test measures scheduler latency as well as the backoff, so the release
package cannot be built.

## What Changes

- Split the retry-backoff wait into a pure delay calculation and an injectable wait function. The
  production default stays a context-aware timer, so runtime behaviour does not change.
- Expose a test-only hook (`export_test.go`) that replaces the wait with a recorder. The
  recorder captures each requested backoff delay and returns right away.
- Rewrite `TestDoRequest_NoBackoffOnFinalAttempt` so it asserts on the **recorded delays**
  (exactly `[base, 2*base]` for three attempts, with no third wait) instead of elapsed wall-clock
  time.
- Convert `TestDoRequest_RetriesUseBackoff` to the same recorded-delay assertion, so it no longer
  relies on wall-clock gaps either.
- Add a deterministic test that the per-attempt delay is clamped to the cap.
- Keep `TestDoRequest_BackoffRespectsContextCancellation` on the real timer. It must prove that
  the real wait honours cancellation, and its bound (5s against a 30s backoff) has wide margin.

## Non-goals

- No change to retry count, base backoff, cap, or which errors are retriable.
- No sweep of other timing-based tests in the repo, such as the `DialerTimeout` 2s bound in
  `pkg/cache/upstream/cache_test.go`. Those can be handled separately if they flake.
- No nixpkgs-side change, such as a `checkFlags` skip or a patch. Once this lands in an ncps
  release, the nixpkgs package bump (or a `fetchpatch` of this commit) closes the issue.
- No new clock abstraction library or dependency.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `upstream-fetch-resilience`: the transient-retry requirement gains a scenario that states an
  existing guarantee explicitly: no backoff wait follows the final attempt.

## Impact

- **Code**: `pkg/cache/upstream/cache.go` (backoff wait refactor, new unexported field),
  new `pkg/cache/upstream/export_test.go`, `pkg/cache/upstream/backoff_guard_test.go`,
  `pkg/cache/upstream/backoff_test.go`.
- **Runtime I/O / network latency / memory**: none. The production wait path is the same timer
  and `select`. The only addition is one function-pointer field per `upstream.Cache`.
- **Tests**: the upstream backoff tests become deterministic and faster, since they no longer
  sleep, and stop depending on builder load. This unblocks the nixpkgs aarch64 build.
- **APIs**: no exported API change. The hook exists only in the `_test.go` build.
