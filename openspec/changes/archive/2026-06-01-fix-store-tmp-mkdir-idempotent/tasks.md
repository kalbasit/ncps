## 1. Tests (Red)

- [x] 1.1 In `pkg/storage/local/local_test.go`, add a test `TestSetupDirsIdempotent` that calls `setupDirs()` twice on the same temp directory and asserts both calls return nil
- [x] 1.2 Add a test `TestSetupDirsPreservesExistingTmpFiles` that creates a partial file in `store/tmp/`, calls `setupDirs()`, and asserts the file still exists and the call returns nil
- [x] 1.3 Run `task test` — confirm both new tests fail (red)

## 2. Implementation (Green)

- [x] 2.1 In `pkg/storage/local/local.go` `setupDirs()`, remove the `os.RemoveAll(s.storeTMPPath())` call and its error check (lines 629–631)
- [x] 2.2 Run `task test` — confirm both new tests pass (green)

## 3. Verification

- [x] 3.1 Run `task fmt` and apply any formatting changes
- [x] 3.2 Run `task lint` and fix any issues
- [x] 3.3 Run `task test` — confirm all tests pass
