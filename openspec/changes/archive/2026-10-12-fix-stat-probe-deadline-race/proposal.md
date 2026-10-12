## Why

The bounded NAR presence probe (#1479) guarantees that a stalled storage probe is reported as
*indeterminate*, never as *absent*. That guarantee does not hold for context-aware backends (S3):
the deadline handed to the backend and the caller-side wait timer share the same duration, so the
backend can return its own `context deadline exceeded` first. That error is passed through
unclassified, and the read path then treats it as a confirmed miss — a spurious 404, or a phantom
NAR in upload-only mode. CI caught this as a flaky `TestStatProbeDeadlineReachesBackend` (PR
#1524). It is a regression introduced in the 0.10 cycle and must be fixed before v0.10.0.

## What Changes

- When the presence probe's own deadline expires and the backend reports that expiry as an error,
  the outcome SHALL be classified as a probe timeout (indeterminate), exactly as if the caller-side
  timer had fired first.
- That outcome is counted in the existing probe-timeout metric and logged like any other timeout.
- A caller that cancels its own request still gets its own cancellation error, not a probe timeout.
- `TestStatProbeDeadlineReachesBackend` becomes deterministic, with a test that forces the
  backend to win the race.

## Non-goals

- Changing the probe timeout default, the per-request budget, or probe coalescing.
- Reclassifying other, non-deadline storage errors (they keep their existing treatment).
- Any change to the local filesystem backend, whose probe cannot observe cancellation.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `nar-serving-latency-bounds`: the "timed-out presence probe MUST NOT be reported as asset
  absence" requirement now explicitly covers a backend that reports the probe deadline itself.

## Impact

- Code: `pkg/cache/cache.go` (`boundedStatNar`), `pkg/cache/nar_stat_timeout_internal_test.go`.
- No API, configuration, schema or dependency changes.
- I/O, network latency, memory: none. Only how an already-expired probe is classified changes;
  no additional storage calls or allocations on the hot path.
