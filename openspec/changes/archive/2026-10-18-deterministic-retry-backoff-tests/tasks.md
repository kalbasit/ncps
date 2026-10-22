## 1. Red: deterministic tests against a missing seam

- [x] 1.1 Add `pkg/cache/upstream/export_test.go` (package `upstream`) declaring `SetRetryWaitForTest(fn func(context.Context, time.Duration) error)` that assigns `c.retryWait`; verify `go test ./pkg/cache/upstream/...` fails to compile because the `retryWait` field does not exist yet
- [x] 1.2 Rewrite `TestDoRequest_NoBackoffOnFinalAttempt` in `backoff_guard_test.go` to inject a mutex-guarded delay recorder and assert `rt.count == 3`, an error is returned, and recorded delays equal exactly `[base, 2*base]`; verify it is still red (compile failure) for the right reason
- [x] 1.3 Rewrite `TestDoRequest_RetriesUseBackoff` in `backoff_test.go` to assert exactly one recorded delay equal to `backoff` (drop the wall-clock gap check and unused timestamp recording); verify red
- [x] 1.4 Add `TestDoRequest_BackoffDelayIsCapped` using a base above the 2s cap and asserting every recorded delay is `<=` the cap; verify red

## 2. Green: split delay computation from waiting

- [x] 2.1 In `pkg/cache/upstream/cache.go`, extract pure `retryBackoffDelay(attempt int) time.Duration` (existing doubling + cap + overflow guard) and package-level `sleepCtx(ctx, d) error` (existing timer/select moved verbatim); verify `go build ./...` succeeds
- [x] 2.2 Add unexported `retryWait func(context.Context, time.Duration) error` to `Cache`, default it to `sleepCtx` in `New`, and make `waitRetryBackoff` keep the `base <= 0` short-circuit then call `c.retryWait(ctx, c.retryBackoffDelay(attempt))`; verify all tests from group 1 pass and `TestDoRequest_BackoffRespectsContextCancellation` still passes against the real timer
- [x] 2.3 Sanity-check the guard: temporarily remove the `i < defaultHTTPRetries-1` condition in `doRequest`, confirm `TestDoRequest_NoBackoffOnFinalAttempt` fails with 3 recorded delays, then restore it and confirm green

## 3. Robustness verification

- [x] 3.1 Run the upstream backoff tests under load to prove load-independence: `go test ./pkg/cache/upstream/ -run 'TestDoRequest_' -count=200 -race -cpu 1,2` and verify zero failures
- [x] 3.2 Run `nix build .#ncps` (runs the package's checkPhase, as nixpkgs does) and verify it succeeds; confirm `vendorHash` is unchanged (no new imports from new packages)

## 4. Completion gates

- [x] 4.1 Run `task fmt`, `task lint`, and `task test`; verify each exits 0
- [x] 4.2 Reference NixOS/nixpkgs#574569 in the ncps PR description, and note that the nixpkgs fix is a version bump or `fetchpatch` of this commit once released
