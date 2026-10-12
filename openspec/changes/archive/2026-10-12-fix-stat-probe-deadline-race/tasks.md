## 1. Regression tests (RED)

- [x] 1.1 Add a test where the storage backend immediately returns a wrapped `context.DeadlineExceeded` from its presence probe, and assert the bounded probe returns an error matching `ErrStatTimeout`. Confirm it fails on main.
- [x] 1.2 Add a test asserting that `statNarInStore`/`narServability` classify that outcome as indeterminate, not "not present".
- [x] 1.3 Add a test where the caller cancels while a context-honouring probe is pending, and assert the result is `context.Canceled`, not `ErrStatTimeout`.

## 2. Fix (GREEN)

- [x] 2.1 Extract the timer branch's timeout reporting (metric, span event, log line, wrapped `ErrStatTimeout`) into a helper.
- [x] 2.2 In the result branch of `boundedStatNar`, classify `context.DeadlineExceeded` from the backend through the timeout helper, keeping the backend cause wrapped.
- [x] 2.3 Run the new tests and `TestStatProbeDeadlineReachesBackend` repeatedly (`-count=200 -race`) to confirm they are no longer flaky.

## 3. Verify

- [x] 3.1 `task fmt`, `task lint`, `task test` all exit zero.
