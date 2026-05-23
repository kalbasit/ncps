## Why

`nix flake check` takes ~13 minutes on CI and is on the critical path for every
PR merge. The slowness comes from structural choices in the check graph rather
than from the underlying tests themselves: a single monolithic `packages.ncps`
derivation runs the full race-enabled Go suite alongside four ephemeral
service backends (Garage, PostgreSQL, MariaDB, Redis) brought up serially in
`preCheck`; several small lint/drift checks (`golangci-lint-check`,
`ent-codegen-drift-check`, `atlas-sum-check`, `ent-lint-check`) each override
`buildGoModule` and pay a full vendor + compile cost; and `checks` includes
every `devShell` and `package`, multiplying derivations that are not really
quality gates. Cutting this wall-clock without removing coverage frees minutes
per PR across the team.

## What Changes

- Split the monolithic `packages.ncps` check into multiple Nix derivations so
  Nix can fan them out in parallel and cache them independently:
  - Pure-Go unit tests (no service dependencies) as their own derivation.
  - Integration tests grouped by required backend (S3, Postgres, MySQL,
    Redis), so a backend is only started for the tests that need it.
- Stop re-doing Go vendor + compile in the tiny lint/drift checks. Build the
  helper binaries (`atlas-sum-check`, `ent-lint`) once and reuse, or run them
  via a shared `goModules` output / a single `stdenvNoCC` wrapper.
- Remove `self'.packages` and `self'.devShells` from the `checks` attrset
  unless they add real signal; keep only what we actually want to gate on.
- De-duplicate the Postgres/MySQL spin-up between `packages.ncps` and
  `schema-equivalence-check` (factor a shared `mkWithDatabases` helper).
- Profile the new check graph end-to-end and record the before/after wall
  times in `openspec/changes/lean-flake-check/` (same pattern as
  `less-tests`).

## Capabilities

### New Capabilities

- `flake-check-topology`: defines the structure of `nix flake check` — which
  derivations exist, what each one covers, what services it depends on, and
  the invariants the graph must preserve (no coverage loss, no lint
  loosening, no skipped checks).

### Modified Capabilities

- `test-suite-efficiency`: existing requirements about keeping the Go test
  suite fast still apply; this change extends them to the *derivation
  granularity* at which those tests run under Nix.

## Impact

- `nix/checks/flake-module.nix`: restructured.
- `nix/packages/ncps/default.nix`: `checkPhase` / `preCheck` split or
  parametrized so a "unit-tests-only" build is possible.
- `nix/packages/ncps/pre-check-*.sh` / `post-check-*.sh`: likely reused
  unchanged but composed differently.
- CI: `nix flake check -L` is invoked by the shared reusable workflow at
  `kalbasit/gh-actions/.github/workflows/ci.yml` (this repo's
  `.github/workflows/ci.yml` is now a thin caller). The invocation itself is
  unchanged; per-derivation parallelism is handled by Nix, so the speedup is
  picked up automatically by both this repo and any other consumer of the
  reusable workflow.
- No change to runtime binary behavior, no change to test assertions, no
  change to lint rules.

## Non-goals

- Reducing what is tested (no skipping race detector, no dropping integration
  backends, no narrowing lint set).
- Replacing Nix as the CI driver, or moving checks out of `nix flake check`.
- Caching strategy changes outside the flake itself (Cachix config,
  GitHub Actions cache tuning).
- Helm chart test changes (`helm-unittest-check` is already cheap).
