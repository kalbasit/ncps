## Context

The `e2e-nightly` workflow (added in #1383) first ran on 2026-06-10 (run 27252786882) and failed on both matrix legs:

- **local: 0/9 passed.** Every scenario aborted before any test with `deps: services not ready within 120s`. Reproduced locally: `nix/e2e-tests/src/runner.py::_run_local` calls `Deps.ensure_up()` and `Deps.teardown()` *inside* the per-scenario loop of `run_scenarios`. Each backend's start script (`nix/process-compose/start-{postgres,mysql,garage,redis}.sh`) provisions an ephemeral `mktemp -d` data dir, so every scenario pays a full cold boot (`initdb`, `mariadb-install-db`, garage layout). On a dev box this cold boot is ~70s; on a hosted `ubuntu-24.04` runner it exceeds the 120s readiness timeout (`deps.py`), so all 9 local scenarios time out identically. The harness emits no backend logs, so CI shows only the opaque timeout.
- **kubernetes: 13/15 passed, 1 skipped, 1 failed.** The `already exists` lines are non-fatal idempotent re-provisioning noise. Only `single-local-mariadb` failed, on one check: `HTTP Endpoints: Failed to fetch narinfo … HTTP 500` — while `healthz` passed and Database (2 NAR entries) and Storage (2 NAR files) passed, and the same hashes fetched fine in an adjacent mariadb scenario. `k8s_tests_tester.test_http_endpoints` does `port-forward` → `sleep 3` → one `healthz` probe → a **single, no-retry** narinfo GET; a transient post-deploy 5xx (readiness/seeding race, consistent with known MariaDB-on-Kind flakiness) fails the whole scenario.

## Goals / Non-Goals

**Goals:**
- Make the `local` leg reliable on a hosted runner: 0/9 → all-pass.
- Make the `kubernetes` validation robust to a transient post-deploy error.
- Make readiness failures self-diagnosing in CI.

**Non-Goals:**
- No ncps production code changes — harness reliability only.
- Not investigating whether the MariaDB 500 is a genuine ncps bug; the transient, single-occurrence signal is treated as flakiness and absorbed by a bounded retry. A persistent 5xx still fails.
- Not switching backends to persistent (warm) data dirs — start-once removes the redundant cold boots without changing the ephemeral-dir design.

## Decisions

- **Start backends once per multi-scenario local run.** Hoist the deps lifecycle out of `_run_local` into `run_scenarios` for `local` mode: start once before the loop, tear down once after. `_run_local` keeps managing deps for the single-scenario `run_scenario` path. `needs_redis` for the shared startup is the OR across all selected local-supported scenarios.
  - *Alternative considered:* persistent/named data dirs so scenario 2+ boot warm. Rejected — larger blast radius, leaves a race on the 120s timeout for scenario 1's cold boot, and complicates cleanup.
  - *Alternative considered:* rely only on `ensure_up`'s "ports already reachable → leave as-is" short-circuit by not tearing down between scenarios. This is effectively what start-once formalizes; doing it explicitly keeps teardown ownership unambiguous.
- **Raise the readiness timeout to 300s.** A single cold boot on a slow runner must fit even in the single-scenario path. 300s is comfortably above the observed ~70s dev cost with headroom for a constrained runner.
- **Emit diagnostics on readiness timeout.** Before raising, dump `process-compose process list` and per-process logs (via the process-compose API/CLI on the control port) so CI identifies the unready backend.
- **Bounded retry on kubernetes validation fetches.** Wrap the narinfo and NAR GETs in a small retry (e.g. up to ~5 attempts, short backoff) that retries connection errors and 5xx. A persistent failure still fails the check.

## Risks / Trade-offs

- [Start-once changes deps ownership] → Keep teardown in a `finally` around the whole loop so backends are always stopped even if a scenario raises; the single-scenario path is unchanged.
- [Retry could mask a real, reproducible 5xx] → Bound attempts and only retry connection errors / 5xx; a deterministic failure still surfaces. If `single-local-mariadb` 500s persistently after this, that is a separate, real ncps bug to be filed.
- [Longer timeout hides a genuinely hung backend] → Mitigated by the new diagnostics: a timeout now prints which backend was unready and its logs.

## Migration Plan

Harness-only change; no deployment or rollback concerns. Validate with `nix run .#e2e -- --mode local --all` (expect all-pass) and a manual re-dispatch of the `e2e-nightly` workflow.

## Open Questions

- None blocking. Whether the MariaDB-local narinfo 500 is ever reproducible deterministically is deferred; if the retry stops absorbing it, file a dedicated ncps serve bug.
