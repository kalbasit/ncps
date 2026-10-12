## Context

`boundedStatNar` runs the backend probe inside a singleflight call with
`probeCtx = context.WithTimeout(context.WithoutCancel(ctx), timeout)` and, on the caller side,
selects on the shared result, the caller's `ctx.Done()`, and `time.NewTimer(timeout)`. Only the
timer branch produces `ErrStatTimeout`. The result branch returns the backend's error as-is.

Because both deadlines have the same duration and the singleflight goroutine and the caller's
timer are scheduled independently, a context-aware backend (S3, or the test double) can deliver
`context.DeadlineExceeded` on the result channel first. `narServability` only treats
`ErrStatTimeout` as indeterminate, so that path falls through as "not in store".

`probeCtx` is detached from the caller (`WithoutCancel`), so a deadline error coming back from the
backend can only mean that the probe's own bound expired (or a backend-internal timeout fired,
which is the same "could not determine" outcome). It never carries the caller's cancellation.

## Goals / Non-Goals

**Goals:**

- A backend-reported deadline is classified as `ErrStatTimeout`, with the same metric, span event
  and log line as the timer branch.
- The caller's own cancellation still wins and is reported as `ctx.Err()`.
- A deterministic regression test.

**Non-Goals:**

- Retuning timeouts, the budget, coalescing, or the capacity limit.
- Reclassifying non-deadline backend errors.

## Decisions

1. **Classify in the result branch, not by skewing the deadlines.** We could give `probeCtx` a
   slightly longer deadline than the timer so the timer always wins. That only makes the race less
   likely: scheduling delays are unbounded, and a backend-internal timeout would still slip through.
   Classifying `errors.Is(res.err, context.DeadlineExceeded)` is exact.
2. **Caller cancellation takes precedence.** The probe context is detached from the caller, so
   the backend's deadline error never carries the caller's cancellation. A parked caller is woken
   through the `ctx.Done()` case. But if the caller cancels before it reaches the `select` and the
   result is already buffered, both cases are ready and Go picks one at random. So the result
   branch checks `ctx.Err()` before classifying a deadline, and returns the caller's error when it
   is set. A test drives that window with an already-cancelled caller.
3. **One reporting helper.** Move the timer branch's metric, span event, log line and error into a
   helper, and call it from both branches. The two timeout paths then cannot drift apart. The
   metric `reason` stays `deadline` for both.
4. **Wrap, don't replace.** Return `fmt.Errorf("%w after %s: %w", ErrStatTimeout, elapsed, err)`
   (Go 1.20+ multi-`%w`) so callers match `ErrStatTimeout` and the backend cause stays visible in
   logs.

### Test strategy (TDD)

- RED: a backend double that immediately returns a wrapped `context.DeadlineExceeded`, the same
  result a context-aware backend gives when its deadline wins the race. The caller-side timer
  cannot fire first, so this deterministically takes the result branch. Assert
  `errors.Is(err, ErrStatTimeout)`. This fails on main.
- Keep `TestStatProbeDeadlineReachesBackend`; it becomes non-flaky once the fix lands.
- Add a caller-cancellation test: the caller cancels while a backend-honouring probe is pending, and
  the call returns `context.Canceled`, not `ErrStatTimeout`.

## Risks / Trade-offs

- A backend that returns `DeadlineExceeded` for an unrelated internal reason is now reported as
  "undetermined" instead of "absent". That is the safer reading, and it is what the spec requires.
