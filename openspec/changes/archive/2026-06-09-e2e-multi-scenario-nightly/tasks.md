## 1. Multi-scenario selection (CLI + runner)

- [x] 1.1 Write failing tests for `cli.build_parser`: `--scenario` is repeatable (`action="append"`), comma values split, `--all` flag present, and `--all` + `--scenario` together is a usage error
- [x] 1.2 Write failing tests for a new `runner.run_scenarios(mode, names, verbose)`: resolves `--all` to every catalog scenario, runs each, aggregates exit code (non-zero iff any FAIL, SKIP alone is zero), and prints a per-scenario summary
- [x] 1.3 Update `cli.py`: make `--scenario` `action="append"`, add `--all`, comma-split values, enforce mutual exclusion, and route to `run_scenarios`
- [x] 1.4 Add `runner.run_scenarios` wrapping the existing single-scenario path; keep `run_scenario` for the single case; emit the summary table
- [x] 1.5 Confirm single `--scenario <name>` behavior is unchanged (regression test) and `--list` still works
- [x] 1.6 Update `nix/e2e-tests/README.md` usage section with `--all` and repeatable `--scenario`

Note: also added `checks.e2e-harness-unit` (flake check) and `task test:e2e:unit` so the harness now has a reproducible offline unit-test net (it had none).

## 2. Nightly workflow with commit dedup

- [x] 2.1 Add `.github/workflows/e2e-nightly.yml` with `schedule` (04:00 UTC cron) and `workflow_dispatch` triggers; no `pull_request`/`push` triggers
- [x] 2.2 Add a `gate` job: resolve current `main` SHA, probe an `actions/cache` key `e2e-nightly-tested-<sha>`; output `skip=true` on cache hit, `skip=false` on miss or `workflow_dispatch`
- [x] 2.3 Add the matrix job over `mode ∈ {local, kubernetes}` gated on `needs.gate.outputs.skip != 'true'`, with `fail-fast: false`, each leg running `nix run .#e2e -- --mode <mode> --all` (Cachix/Nix setup mirroring `ci.yml`)
- [x] 2.4 Add a final `record` job (needs all matrix legs, runs only if all succeeded) that writes the `e2e-nightly-tested-<sha>` cache key so the next schedule short-circuits
- [x] 2.5 Validate the workflow YAML with `actionlint`
- [x] 2.6 Document the nightly workflow and the commit-dedup behavior in `nix/e2e-tests/README.md` (CI section)

## 3. Verification

- [x] 3.1 `task fmt` and `task lint` clean (including the harness Python and workflow YAML)
- [x] 3.2 `task test` (Go unit tests) green — no production code touched, confirm no regressions
- [x] 3.3 Harness unit check green: `nix build .#checks.<system>.e2e-harness-unit`
- [x] 3.4 Local-mode smoke: `nix run .#e2e -- --list` and a single-scenario run still behave; multi-scenario selection parses (covered by unit tests)
- [x] 3.5 `openspec validate e2e-multi-scenario-nightly --strict` passes

## Deferred (follow-up change)

The kubernetes pin-lift (`KubernetesDeployment` adapter; running `cdc-lifecycle`
and `staging-contention` in `--mode kubernetes`) is carved out — see `design.md`
D2 and the proposal's deferred note. Tracked for a dedicated follow-up where the
slow Kind verification can iterate.
