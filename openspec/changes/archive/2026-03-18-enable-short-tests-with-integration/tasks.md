## 1. Profile test execution times

- [x] 1.1 Run `go test -race ./...` and capture test timing information
- [x] 1.2 Identify tests that take >500ms to complete

## 2. Add testing.Short() guards to slow tests

- [x] 2.1 Add `testing.Short()` guards to slow tests identified in step 1
- [x] 2.2 Verify the guards work correctly

## 3. Verify short test mode works

- [x] 3.1 Run `go test -short -race ./...` and verify it completes faster
- [x] 3.2 Verify no regressions in the tests that still run
