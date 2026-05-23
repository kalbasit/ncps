## Context

`nix flake check` is part of the gate the reusable CI workflow
(`kalbasit/gh-actions/.github/workflows/ci.yml`) runs against every PR.
The relevant CI jobs are `shared / build / build (x86_64-linux)` and
`shared / build / build (aarch64-linux)`, which run in parallel and take
~13–14 minutes each (observed: 768s / 861s on run 26343867348 of `main`).
These jobs do *both* `nix flake check` and the Docker image build in one
shot, so the ~13 min figure is an upper bound on what this change can
move — the Docker build is unaffected floor time. The dedicated
`shared / flake-check` job is gated by `if: ${{ inputs.oci == false }}`
in the upstream `kalbasit/gh-actions/.github/workflows/ci.yml`; ncps
sets `oci: true`, so that job is intentionally skipped and the flake
check work happens inside the build job instead. It is not on the
critical path.

The bottleneck inside the flake-check half of those jobs is structural,
not test-internal:

- `packages.ncps` (in `nix/packages/ncps/default.nix`) is one derivation
  whose `checkPhase` runs `go test -race -coverprofile=coverage.txt ./...`
  while `preCheck` starts Garage + Postgres + MariaDB + Redis serially.
  Nix cannot parallelize anything inside a single derivation, so the entire
  graph waits on this one node.
- Four lint/drift checks (`golangci-lint-check`, `ent-codegen-drift-check`,
  `atlas-sum-check`, `ent-lint-check`, in `nix/checks/flake-module.nix`)
  each `overrideAttrs` `packages.ncps` → each re-vendors Go, re-fetches
  modules, and re-compiles the binary just to run a small tool.
- `schema-equivalence-check` separately spins Postgres + MySQL up *again*
  to run one `TestSchemaEquivalence`.
- `checks = self'.packages // self'.devShells // {...}` drags every
  devShell + every package into the check set, multiplying derivations
  with no quality signal.

Constraints:

- Race detector stays on for every test that currently has it.
- Every integration backend currently exercised stays exercised.
- All lint rules stay enforced; no `--skip` additions.
- `nix flake check` must remain the single entry point — the reusable
  workflow at `kalbasit/gh-actions` is shared with other repos and we
  can't (and don't want to) change its invocation.
- Coverage output for codecov must keep working
  (`nix build .#ncps.coverage` is invoked separately by the reusable
  workflow).

## Goals / Non-Goals

**Goals:**

- Cut `nix flake check` wall-clock on a cold cache by at least 40% on
  CI hardware, measured against a recorded baseline.
- On a warm cache, ensure that a change touching only one Go package
  (or only one DB backend's integration tests) invalidates only the
  derivations that actually depend on that input.
- Make the check graph legible: each derivation has a single,
  named-by-purpose responsibility (unit, S3, Postgres, MySQL, Redis,
  schema-equivalence, lint, drift, helm).
- Run the lint/drift tooling without re-vendoring Go each time.

**Non-Goals:**

- Trimming what is tested. No race-detector drop, no skipped backends,
  no lint loosening. (Enforced by spec requirements.)
- Replacing Nix as the driver, or restructuring the reusable CI
  workflow.
- Tuning Cachix or GHA cache settings.
- Helm chart test changes — `helm-unittest-check` is already cheap.
- Cross-platform parallelism (e.g., farming aarch64 work out). The
  reusable workflow already handles per-system fan-out.

## Decisions

### D1. Split `packages.ncps` checks by required backend, not by Go package

Introduce one check derivation per *backend cohort*:

- `ncps-unit-tests` — no services. Runs all tests that have no
  integration build tag.
- `ncps-s3-tests` — starts Garage only.
- `ncps-postgres-tests` — starts Postgres only.
- `ncps-mysql-tests` — starts MariaDB only.
- `ncps-redis-tests` — starts Redis only.

Selection is by **Go build tag**, not by `-run` regex. Each integration
file gets `//go:build integration_s3` (etc.). The unit derivation
builds with the default tags; each integration derivation builds with
its own tag added. Build tags are robust to test renames and let
`go vet` / `golangci-lint` reason about which files belong to which
build.

**Alternatives considered:**

- *Per-package derivations* (`pkg/cache`, `pkg/storage/s3`, …). Too
  fine-grained: cache invalidation per package is good, but startup
  cost per derivation (Go compile, race-detector instrumentation,
  service spin-up where applicable) outweighs the parallelism win, and
  many packages have no service dependency anyway.
- *`-run` / `-skip` regex selection*. Brittle: relies on a naming
  convention that nothing enforces. Drifts silently the first time a
  test is renamed.
- *Single derivation, parallel `preCheck`*. Doesn't help: the Nix-level
  bottleneck is the one-derivation serialization of the whole check.
  Internal parallelism is already on (`t.Parallel()`).

### D2. Build lint/drift helper binaries once; consume them from `stdenvNoCC` checks

Add a small `packages.ncps-checktools` derivation (or `passthru.checktools`
on `packages.ncps`) that does one `buildGoModule` and emits:
`bin/ent-lint`, `bin/atlas-sum-check`. The corresponding checks become
`stdenvNoCC.mkDerivation` with `nativeBuildInputs = [ ncps-checktools ]`
and a one-liner `buildPhase`. No `overrideAttrs` of the main package, no
re-vendoring.

`golangci-lint-check` and `ent-codegen-drift-check` still need the Go
toolchain + module cache (they invoke `go generate` / a full-tree type
check), so they keep using `buildGoModule` — but should `inherit src
vendorHash` from `packages.ncps` so they hit Cachix on identical inputs
instead of producing distinct fixed-output derivations per check.

### D3. Drop the main `packages.ncps` `doCheck = true`; coverage moves to a dedicated derivation

Today the main binary build *is* the test run. After D1, tests live in
the new per-backend derivations. The main `packages.ncps` becomes a
pure build (`doCheck = false`) — fast, cacheable, and what every
downstream consumer actually wants from `nix build .#ncps`.

Coverage stays as a separate derivation invocable as
`nix build .#ncps.coverage` (CI already calls it explicitly). It runs
the union of the per-backend test suites with `-coverprofile` and
merges the per-cohort `cover.out` files into a single `coverage.txt`.
It is **not** in the `checks` set — `nix flake check` doesn't pay for
coverage instrumentation.

### D4. Extract a `mkDbBackedCheck` helper; de-duplicate `pre-check-*.sh` invocations

`schema-equivalence-check` and the new per-backend integration
derivations share the same Postgres/MySQL/Redis/Garage spin-up scripts
under `nix/packages/ncps/`. Factor a Nix helper:

```nix
mkDbBackedCheck { name, backends, checkPhase }
```

that wires the right `pre-check-*.sh` / `post-check-*.sh` pair into
`preCheck` and registers a single cleanup trap. `schema-equivalence-check`
becomes a one-line call to it.

### D5. Replace `checks = self'.packages // self'.devShells // {...}` with an explicit list

The `// self'.devShells` clause adds N derivations that are not quality
gates — they're already realised by anyone running `nix develop`. Remove
both `self'.packages` and `self'.devShells` from the `checks` attrset
and enumerate exactly the named checks. The main `packages.ncps` build
is implicitly exercised by every check derivation that depends on it,
so we lose nothing by not listing it as its own check.

### D6. Baseline + measure (mirror the `less-tests` pattern)

Before any structural change, record per-derivation wall times on CI
hardware (or a local CI-shaped runner) and commit them to
`openspec/changes/lean-flake-check/baseline-timings.txt`. Re-measure
after each phase. Same shape as `openspec/changes/archive/2026-05-23-less-tests/`.

## Risks / Trade-offs

- **[Build-tag tax]** Every integration test file needs a build-tag
  header; a missing tag silently demotes a test to the unit cohort,
  losing service coverage. → **Mitigation**: add a one-shot codemod
  that tags all current integration files; add a CI lint
  (`go list -tags=... ./... | diff`) that fails if any file in the
  integration directories lacks a tag.
- **[Cachix re-warming]** New derivation names mean a cold Cachix until
  the next push to `main`. → **Mitigation**: land behind a feature
  branch, push once to warm Cachix, then merge.
- **[Per-derivation startup overhead]** Spinning Postgres four times
  (once per Postgres-touching check) loses against starting it once
  for a giant test if the per-Postgres cohort is small. → **Mitigation**:
  this is exactly what D1's *backend-cohort* granularity (vs.
  per-package) is designed to avoid: at most one derivation per
  backend.
- **[Coverage drift]** Splitting tests across derivations risks
  losing coverage from cross-package interactions. → **Mitigation**:
  the dedicated `ncps.coverage` derivation in D3 still runs the
  full union and merges profiles, so codecov sees the same shape as
  today.
- **[Reusable workflow assumes one job]** The CI gate in
  `kalbasit/gh-actions` expects `nix flake check` as a single step;
  splitting into many derivations is fine (Nix handles it internally)
  but breaks the assumption that any one derivation is the long pole
  — log readability changes. → **Mitigation**: documented in tasks.md;
  no upstream change required.

## Migration Plan

Forward-only, one phase per PR:

1. **Phase 1 — Baseline.** Add `baseline-timings.txt` capturing each
   current check's wall time. No behavior change.
2. **Phase 2 — Helper binaries (D2).** Introduce `ncps-checktools` and
   switch `atlas-sum-check` + `ent-lint-check` to `stdenvNoCC`
   consumers. Re-measure; commit `after-checktools-timings.txt`.
3. **Phase 3 — Per-backend integration cohorts (D1).** Add build tags
   to integration files; introduce `ncps-{s3,postgres,mysql,redis}-tests`
   derivations and `ncps-unit-tests`. Keep the monolith as
   `ncps-all-tests-compat` (gated off `checks` but invocable) for one
   release cycle. Re-measure.
4. **Phase 4 — Helper extraction (D4).** Land `mkDbBackedCheck`,
   migrate `schema-equivalence-check`. Re-measure.
5. **Phase 5 — Coverage split (D3).** Set `doCheck = false` on
   `packages.ncps`; move coverage to its own derivation that merges
   profiles. Re-measure.
6. **Phase 6 — Prune (D5).** Drop `self'.packages // self'.devShells`
   from `checks`. Remove `ncps-all-tests-compat`. Final measurement
   commits `final-timings.txt`.

Rollback: each phase is a single PR; revert that PR. The monolith path
remains intact through Phase 3 to keep rollback cheap.

## Open Questions

- **Build-tag naming.** `integration_s3` vs `integration` (single tag)
  vs `integration_garage`? Single tag is simpler but loses
  per-backend selection. Recommendation: per-backend (`integration_s3`,
  `integration_postgres`, …) — leave decision to the spec.
- ~~Drop `aarch64-darwin` from `flake.nix` `systems`?~~ **Resolved:
  keep it.** Darwin is intentionally listed so downstream consumers
  can import ncps's flake on darwin; CI does not run it (verified
  across recent main runs) and therefore it contributes 0s to flake
  check wall time. Not a lever.
- **Should `nix flake check` invoke the merged coverage build, or
  should it stay out-of-band?** Today CI builds `.#ncps.coverage`
  separately. Decision in this design: keep it out-of-band.
  Worth confirming with the reusable-workflow author.
