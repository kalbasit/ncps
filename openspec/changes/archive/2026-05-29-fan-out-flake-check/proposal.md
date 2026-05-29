# Proposal: fan-out-flake-check

## Why

After compile-once (#1292) CI is ~10m, bottlenecked by a single `nix flake check`
that builds ~12 check derivations on **one 4-core runner**, where they starve
each other for cores. The work is logically parallel (each check is independent)
but CPU-serialized. The remaining costs — backend-cohort test execution,
`golangci-lint-check` (~2.3m, its own analysis), `ent-codegen-drift-check` — are
not further reducible by caching; they need more cores, in parallel.

## What Changes

- Replace the monolithic `nix flake check` with a **CI matrix**: enumerate the
  check attrs via `nix eval .#checks.<system> --apply builtins.attrNames` and run
  each as its own parallel job (`nix build .#checks.<system>.<name> -L`) on its
  own free runner. Wall-clock collapses from sum-under-contention to
  **slowest-single-check + nix setup (~3–4m)**.
- The fan-out matrix + a dedicated coverage job live in **ncps's** workflow.
- **gh-actions** (shared reusable workflow) gains a `run_flake_check` input
  (default `true`); ncps sets it `false` so the shared `build` job builds/pushes
  only the OCI image and no longer runs the monolithic check. **(cross-repo)**
- Cachix carries `_ncps-test-cache`/`_ncps-base`/`goModules`, so parallel jobs
  pull the shared compile cache rather than each rebuilding it.

## Capabilities

### New Capabilities
- None.

### Modified Capabilities
- `flake-check-topology` (ncps): add a requirement that CI runs each `checks`
  attr as an independent parallel job (relying on the existing explicit-checks
  enumeration), and that coverage is produced by a job that consumes the
  fanned-out cohort outputs.
- `oci-build` / `reusable-ci` (kalbasit/gh-actions): `build.yml` and the
  orchestrator gain a `run_flake_check` input that gates the in-workflow
  `nix flake check` + coverage steps. Tracked as a sibling gh-actions change.

## Impact

- **Affected:** ncps `.github/workflows/ci.yml` (new `checks` matrix + coverage
  jobs, final-gate wiring); gh-actions `build.yml` + `ci.yml` + their specs. No
  ncps Go code or flake derivation changes.
- **CI wall clock:** ~10m → ~3–4m expected (slowest check + setup), the path to
  the ~5m target.
- **Runner-minutes:** higher total (N jobs each pay ~1–1.5m nix setup), but free
  and parallel for a public repo — wall-clock is what improves.
- **I/O / network / memory:** CI-only; each job pulls the shared cache from
  Cachix once. No ncps runtime impact. Coverage (one merged profile to Codecov)
  is preserved via a dedicated job.

## Non-goals

- Changing which checks/backends/tests run, coverage scope, or the race detector.
- Reworking the flake's check derivations (compile-once already shipped).
- Grouping cheap checks into shared jobs (possible later optimization; start with
  one-job-per-check for simplicity and maximal parallelism).
