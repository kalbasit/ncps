## Why

The unified e2e harness runs exactly one scenario in one mode per invocation, so
validating the whole catalog means dozens of manual runs, and the harness has no
automated coverage at all. Nothing runs the catalog on a schedule, so
regressions in serving correctness surface only when a developer happens to run a
scenario.

## What Changes

- Add **multi-scenario selection**: a repeatable/comma-separated `--scenario`
  and an `--all` flag that runs every catalog scenario supporting the chosen
  mode. The runner executes each selected scenario, reports per-scenario
  PASS/FAIL/SKIP, and exits non-zero if any fails. Single `--scenario <name>`
  behavior is unchanged.
- Add the harness's **first unit-test net**: fast offline pytest coverage of the
  CLI/runner logic, wired as a `nix flake check` (`e2e-harness-unit`) and a
  `task test:e2e:unit` target. (The harness had no tests before.)
- Add a **nightly GitHub Actions workflow** that runs the full catalog as a
  matrix over both modes (`local`, `kubernetes` via Kind on the ubuntu runner),
  using `--all` so every supported scenario is exercised.
- **Skip redundant runs**: the workflow records the `main` commit SHA it last
  tested and exits early when `main` is unchanged since the previous run, so the
  same commit is never e2e-tested two days running.

> **Deferred (follow-up change):** lifting the local-only pins on
> `cdc-lifecycle` and `staging-contention` so they run in `--mode kubernetes`.
> Investigation (see `design.md` D2) showed this needs substantial new
> plumbing — per-pod port-forwarding, a per-dialect in-cluster DB-access path
> (sqlite-via-exec, pg/mysql-via-port-forward), and a CDC enable/disable
> ConfigMap toggle — plus long Kind verification cycles. It is carved out so the
> verified, high-value work here can ship now. The nightly matrix still runs
> `--mode kubernetes --all`; the two pinned scenarios simply SKIP there until the
> follow-up lands.

## Capabilities

### New Capabilities
- `e2e-nightly-ci`: scheduled (nightly) execution of the full e2e catalog as a
  mode matrix, with last-tested-commit deduplication and manual dispatch.

### Modified Capabilities
- `unified-e2e-harness`: multi-scenario / `--all` selection across one
  invocation, with per-scenario PASS/FAIL/SKIP and an aggregate exit code.

## Impact

- **Code**: `nix/e2e-tests/src/cli.py` (arg parsing), `runner.py`
  (`run_scenarios` loop + aggregate exit code), `nix/e2e-tests/tests/` (new unit
  tests), `flake-module.nix` (new `e2e-harness-unit` check), `Taskfile.yml` (new
  unit target). New `.github/workflows/e2e-nightly.yml`.
- **CI**: net-new nightly job plus a fast offline harness unit check added to
  `nix flake check`; no change to the per-PR hot path for the harness *scenarios*
  (those stay opt-in/scheduled). The commit-SHA skip bounds nightly cost to one
  full run per new `main` commit.
- **I/O / network / memory**: no change to ncps runtime behavior — this is
  test-harness and CI orchestration only. Nightly runs pull NARs from upstream
  and provision Kind, but that load is confined to CI runners, off the per-PR
  path.

## Non-goals

- Lifting the local-only pins on `cdc-lifecycle` / `staging-contention`
  (deferred to a follow-up change, per the note above).
- Promoting any harness *scenario* into `nix flake check` (the sub-3-minute bar
  is unchanged; the scenarios stay manual/scheduled — only the new offline unit
  check is added to flake check).
- Parallelizing scenarios within a single harness process (the matrix
  parallelizes at the GitHub Actions job level).
- Adding new test scenarios or changing what any existing phase asserts.
- Changing ncps production behavior, storage, or database semantics.
