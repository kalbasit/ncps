## Context

`(*Cache).waitRetryBackoff` in `pkg/cache/upstream/cache.go` does two things in one function. It
computes `base * 2^attempt` clamped to `retryBackoffCap`, and it blocks on a `time.Timer` that
can be cancelled through the context. `doRequest` calls it after every failed attempt except the
last one (`i < defaultHTTPRetries-1`). Callers can configure only `base`, through
`Options.RetryBackoff`. There is no seam that lets a test see which delays were requested, so the
upstream tests infer them from wall-clock time. That inference is what fails on loaded builders
(see proposal.md - Why).

The package tests are external (`package upstream_test`). The repo's existing pattern for
test-only access is a `package upstream` `export_test.go` file (precedent: `pkg/cache`,
`pkg/storage/s3`, `pkg/server`).

## Goals / Non-Goals

**Goals:**

- Assert the backoff schedule exactly, independent of scheduler load.
- Leave the production code path unchanged in behaviour.

**Non-Goals:**

- A general-purpose fake clock for the package.
- Changing `Options` (the public API).

## Decisions

### D1: Split delay computation from waiting

Extract `func (c *Cache) retryBackoffDelay(attempt int) time.Duration`, a pure function holding
today's doubling and cap logic, including the guard against shift overflow. `waitRetryBackoff`
then does the following:

1. Keeps the `base <= 0 → return ctx.Err()` short-circuit.
1. Calls `c.retryWait(ctx, c.retryBackoffDelay(attempt))`.

Add an unexported field `retryWait func(ctx context.Context, d time.Duration) error` to `Cache`.
`New` sets it to `sleepCtx`, the current timer and `select` body moved verbatim into a
package-level function.

*Why:* the pure delay function can be tested directly for the cap. The injectable wait lets
`doRequest`-level tests record the schedule without sleeping.
*Alternative considered:* asserting on the `attempt` log lines. That would be fragile and
couple the tests to log formatting. *Alternative:* raising the timing margin (for example
`< 20*base`). That only narrows the flake window and does not close it, and Hydra has already
taken 12.5x the expected time.

### D2: Test hook through `export_test.go`

```go
// pkg/cache/upstream/export_test.go
package upstream

func (c *Cache) SetRetryWaitForTest(fn func(context.Context, time.Duration) error) {
    c.retryWait = fn
}
```

*Why:* it follows the existing repo convention and keeps `Options` free of a test-only knob.
*Alternative:* `Options.RetryWait`. That would leak into the public API and the docs for no
production benefit.

### D3: Test rewrites

- `TestDoRequest_NoBackoffOnFinalAttempt`: inject a recorder (guarded by a mutex) that returns
  `nil`. Assert `rt.count == 3`, `require.Error`, and `recorded == []time.Duration{base, 2*base}`.
  The length check (2, not 3) encodes "no wait after final attempt" exactly. With no sleeping,
  the test also runs in about 0ms.
- `TestDoRequest_RetriesUseBackoff`: assert one recorded delay equal to `backoff`, and keep the
  two-attempt success check. Drop `timestampRoundTripper`'s wall-clock gap assertion.
- New `TestDoRequest_BackoffDelayIsCapped`: use a base larger than the cap (for example 5s against
  the 2s default cap) and a recorder, then assert every recorded delay is `<=` the cap. This
  exercises the cap through `doRequest` without changing `Options`.
- `TestDoRequest_BackoffRespectsContextCancellation`: unchanged. It deliberately exercises the
  real `sleepCtx`, and its 5s-against-30s margin is safe.

### D4: TDD ordering

The project requires TDD (`/tdd`). For this test-only fix, the "red" step comes from the
rewritten tests first: they fail to compile because `SetRetryWaitForTest` does not exist yet.
"Green" is the D1 refactor. As a sanity check that the new assertion still guards the property,
temporarily remove the `i < defaultHTTPRetries-1` guard and confirm the test fails with 3
recorded delays. Then restore the guard.

## Risks / Trade-offs

- [The recorder bypasses the real timer, so a bug in `sleepCtx` would go unnoticed by these
  tests] → The cancellation test still runs the real `sleepCtx`. `sleepCtx` is the existing
  code moved without edits.
- [A future refactor could stop routing through `c.retryWait` and silently make the tests
  vacuous] → The recorded-delay length assertions fail if the hook is bypassed (0 recordings
  where 2 are expected).

## Migration Plan

None at runtime. For the nixpkgs issue, cut an ncps patch release that includes this change and
bump `ncps` in nixpkgs, or `fetchpatch` the commit onto 0.10.0. Then close NixOS/nixpkgs#574569
with `Resolves #574569` in the nixpkgs PR.
