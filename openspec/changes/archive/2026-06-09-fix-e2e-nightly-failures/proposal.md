## Why

The first run of the new `e2e-nightly` workflow failed on both legs (run 27252786882). `local` failed systemically (0/9 scenarios) and `kubernetes` failed on a single flaky check (13/15 passed). The nightly is the only automated coverage for the unified e2e harness, so it must be made reliable.

## What Changes

- **Local deps lifecycle — start once per run, not per scenario.** `runner._run_local` currently calls `Deps.ensure_up()`/`teardown()` for *every* scenario. Because each backend (`nix/process-compose/start-*.sh`) uses an ephemeral `mktemp -d` data dir, every scenario pays a full cold boot (`initdb` + `mariadb-install-db` + garage layout). On a hosted runner that cold boot exceeds the 120s readiness timeout, so all 9 local scenarios time out identically. In a multi-scenario (`run_scenarios`) local run the harness will start the backends **once** before the loop (including Redis if any selected scenario needs it) and tear them down **once** after.
- **Tolerate cold hosted-runner boots.** Raise the `ensure_up` readiness timeout (`nix/e2e-tests/src/deps.py`) from 120s to ~300s.
- **Diagnose readiness failures.** On timeout, `ensure_up` MUST surface process-compose state (`process list`) and per-process logs, instead of the current blind "services not ready within 120s".
- **Harden the kubernetes HTTP-endpoint check against transient post-deploy 5xx.** `k8s_tests_tester.test_http_endpoints` does a single, no-retry narinfo GET right after `port-forward` + `sleep 3`; a transient 5xx during ncps warm-up/seeding fails the whole scenario (the `single-local-mariadb` 500). Add a bounded retry (a few attempts with short backoff, retrying connection errors and 5xx) around the narinfo and NAR fetches.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `unified-e2e-harness`: The "Dependency lifecycle and result reporting" requirement is refined — in a local multi-scenario run the harness starts/stops backends once per invocation (not per scenario), waits for readiness with a cold-boot-tolerant timeout, emits backend diagnostics on readiness failure, and the kubernetes validation probes tolerate transient post-deploy 5xx via bounded retry.

## Impact

- Code (harness only, no ncps production code): `nix/e2e-tests/src/runner.py`, `nix/e2e-tests/src/deps.py`, `nix/e2e-tests/src/k8s_tests_tester.py`.
- No change to ncps behavior, APIs, I/O, network latency, or memory usage — this is test-harness reliability only.
- Validation: `nix run .#e2e -- --mode local --all` must go from 0/9 to all-pass; re-dispatch the `e2e-nightly` workflow.
