## 1. Local: start backends once per multi-scenario run

- [x] 1.1 Add a failing pytest in `nix/e2e-tests/tests/test_runner.py` asserting that a `run_scenarios` local run over multiple scenarios calls deps startup once and teardown once (not per scenario), with Redis included when any selected scenario needs it
- [x] 1.2 Hoist the deps lifecycle in `nix/e2e-tests/src/runner.py`: start the managed backends once before the `run_scenarios` local loop and tear them down once after; `_run_local` no longer starts/stops deps when a shared deps instance is provided, but still manages them for the single-scenario `run_scenario` path
- [x] 1.3 Compute `needs_redis` for the shared startup as the OR across all selected local-supported scenarios
- [x] 1.4 Ensure teardown runs in a `finally` around the whole loop so backends stop even if a scenario raises

## 2. Local: cold-boot-tolerant readiness + diagnostics

- [x] 2.1 Add a failing pytest asserting `Deps.ensure_up` default timeout is at least 300s and that a readiness-timeout path emits process-compose state/logs
- [x] 2.2 Raise the `ensure_up` readiness timeout in `nix/e2e-tests/src/deps.py` from 120s to 300s
- [x] 2.3 On readiness timeout, dump the process-compose process list and per-process logs (via the control port) before raising, identifying the unready backend(s)

## 3. Kubernetes: retry transient validation fetches

- [x] 3.1 Add a failing pytest (or extend `nix/e2e-tests/tests/`) asserting the HTTP-endpoint narinfo/NAR fetch retries on connection errors and 5xx and succeeds when a later attempt returns 200
- [x] 3.2 Wrap the narinfo and NAR GETs in `nix/e2e-tests/src/k8s_tests_tester.py::test_http_endpoints` with a bounded retry (≈5 attempts, short backoff) that retries connection errors and 5xx; a persistent failure still fails the check

## 4. Verify

- [x] 4.1 Run the harness pytest suite (`e2e-harness-unit`) and confirm green — 52 passed
- [x] 4.2 Validate the local start-once path end-to-end with a multi-scenario run (`nix run .#e2e -- --mode local --scenario single-local-sqlite --scenario single-local-postgres`): deps boot once and tear down once after the loop, both scenarios PASS with byte-identical NAR serving (2/2). Full `--all` and CI re-dispatch covered by 4.4.
- [x] 4.3 Run `task fmt`, `task lint` and confirm clean
- [ ] 4.4 After merge, re-dispatch the `e2e-nightly` workflow and confirm both legs pass (post-merge)
