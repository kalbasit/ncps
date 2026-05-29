# Design: fan-out-flake-check

## Context

`nix flake check` builds every `checks.<system>.*` derivation on one runner.
ncps's checks are independent (5 cohorts, golangci, ent-drift, atlas-sum,
ent-lint, helm, ncps, ncps-checktools). On a 4-core GitHub runner they contend;
wall-clock ≈ total CPU work / 4. Each is a normal Nix derivation, individually
buildable as `nix build .#checks.<system>.<name>` — so they can each run on their
own free runner instead.

The shared `kalbasit/gh-actions` `build` job (oci) currently runs the monolithic
`nix flake check` + coverage inline. To fan out, that inline check must be
disabled for ncps while the OCI image build stays.

## Goals / Non-Goals

**Goals**
- Run each check as an independent parallel CI job; wall-clock → slowest check.
- Keep OCI image build/push and the single merged Codecov upload working.
- Minimal change to the shared workflow (one opt-in input).

**Non-Goals**
- Changing check contents, coverage scope, or the flake derivations.
- Grouping checks (start one-per-job).

## Decisions

### D1 — Fan-out matrix lives in ncps's workflow
ncps fully controls `.github/workflows/ci.yml`, so the matrix goes there:

```yaml
check-matrix:                     # compute the attr list once
  outputs: { checks: ${{ steps.l.outputs.checks }} }
  steps:
    - checkout; install-nix; cachix
    - id: l
      run: |
        echo "checks=$(nix eval .#checks.x86_64-linux \
          --apply builtins.attrNames --json)" >> "$GITHUB_OUTPUT"
checks:
  needs: check-matrix
  strategy: { fail-fast: false, matrix: { check: ${{ fromJson(needs.check-matrix.outputs.checks) }} } }
  steps:
    - checkout; install-nix; cachix
    - run: nix build ".#checks.x86_64-linux.${{ matrix.check }}" -L
```

`x86_64-linux` matches the `test_systems` scoping from `speed-up-ci` (aarch64
still only builds its image). `fail-fast: false` so one failing check doesn't
cancel the rest. Each cohort starts its own backend *inside* the Nix sandbox
(existing `pre-check-*.sh`), so no service containers are needed.

### D2 — One opt-in input on the shared workflow
`build.yml` + orchestrator `ci.yml` gain `run_flake_check` (bool, default
`true`). When `false`, the `build` job skips its `Flake check`, `Build
coverage`, and Codecov steps and only builds/pushes the OCI image; the
standalone `flake-check` job is also gated. Default `true` keeps every other
consumer unchanged. ncps passes `run_flake_check: false`.

### D3 — Coverage as a dedicated ncps job, after the matrix
Coverage moves out of `build.yml` into an ncps `coverage` job that
`needs: checks` (so the cohorts' `coverage` outputs are in Cachix), then
`nix build .#ncps.coverage` (pulls + merges) and uploads to Codecov. Coverage is
informational, not a gate, so a transient Cachix-propagation miss degrades to a
local rebuild, never a failed merge. Replicates `build.yml`'s exact
`nix build .#ncps.coverage` + `codecov-action@v6` steps.

### D4 — Cachix is the cross-job cache substrate
The `check-matrix`/`checks`/`coverage` jobs use `cachix-action` (pull). The
shared compile cache (`_ncps-test-cache`), `_ncps-base`, and `goModules` are
pushed by prior runs, so parallel jobs pull instead of rebuilding. A cold Cachix
(e.g. after a deps bump) means several jobs each rebuild the base — slower, but
still correct and parallel.

*Alternatives considered:*
- *Fan-out inside gh-actions* (generic matrix in the reusable workflow):
  cleaner for all consumers but a much larger shared-workflow redesign; deferred
  in favor of the minimal `run_flake_check` switch + ncps-local matrix.
- *Bigger runner*: linear, billed even for public repos, caps above 5m.

## Risks / Trade-offs

- **Cold Cachix → duplicate base rebuilds across jobs** → still correct/parallel;
  warm Cachix is the steady state (every merged run pushes `_ncps-test-cache`/
  `_ncps-base`). `check-matrix` only evaluates (`nix eval` does NOT realize
  derivations, so it does not warm the cache); deliberately not building the base
  there, since that would add ~2m to the critical path on every run for a
  cold-cache-only benefit. A cold cache just means a few jobs rebuild the base in
  parallel — accepted.
- **Final-gate wiring**: the ncps `ci` gate must `needs` the new
  `check-matrix`/`checks`/`coverage` jobs and treat `skipped` (fork PRs) as pass.
- **`run_flake_check` drift**: if ncps forgets to set it, checks run twice
  (shared + matrix) — slow but correct.
- **Per-check setup overhead** (~1–1.5m × N jobs): free/parallel; acceptable.

## Migration Plan

1. Land the gh-actions `run_flake_check` input (sibling change) first.
2. ncps: set `run_flake_check: false`; add `check-matrix`/`checks`/`coverage`
   jobs; wire the final gate.
3. Verify on a PR: each check is its own job; wall-clock drops; coverage uploads;
   OCI manifest still assembles.
4. **Rollback**: drop the ncps matrix jobs and unset `run_flake_check` (reverts
   to the monolithic in-build check).

## Open Questions

- Whether to group the trivially-fast checks (atlas-sum, ent-lint, helm) into one
  job to cut setup overhead — defer until CI shows per-job times.
