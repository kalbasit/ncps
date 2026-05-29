# flake-check-topology Specification

## Purpose

This capability defines the structure of `nix flake check` for the
ncps repository — which derivations exist, what each one covers,
what services it depends on, and the invariants the check graph
must preserve as the topology evolves. It exists because the
test-suite wall-clock cost is dominated by Nix-level serialization
and service spin-up, not by Go test work; rules about *which*
derivations a `nix flake check` exposes (and which it doesn't) have
the same load-bearing role for CI cycle time that the
`test-suite-efficiency` rules have for per-test cost.

## Requirements

### Requirement: Backend-cohort granularity for integration test derivations

The set of derivations produced by `nix flake check` SHALL include one
distinct derivation per external-backend cohort exercised by the
integration test suite (S3/Garage, PostgreSQL, MySQL/MariaDB, Redis),
plus a separate derivation for tests with no backend dependency. Each
backend-cohort derivation MUST start exactly the backend it needs and
no others. This replaces the prior topology in which a single
`packages.ncps` derivation started all four backends sequentially in
`preCheck` and ran the entire Go test suite serially in one
`checkPhase`.

#### Scenario: Touching a Go file with no integration dependency

- **WHEN** a developer changes a file under `pkg/` that has no
  integration build tag and runs `nix flake check`
- **THEN** Nix invalidates the unit-test derivation
- **AND** the per-backend integration derivations are served from the
  cache without rebuilding
- **AND** no service backend is started for any cached derivation

#### Scenario: Touching the Postgres preCheck script

- **WHEN** a developer changes `nix/packages/ncps/pre-check-postgres.sh`
  and runs `nix flake check`
- **THEN** Nix invalidates only the `ncps-postgres-tests` derivation
  (and `packages.ncps-coverage`, which is NOT in the checks attrset)
- **AND** only PostgreSQL is started during that derivation's `preCheck`
- **AND** the S3, MySQL, Redis, and unit-test cohorts are served from
  cache

  Note: per-backend cache invalidation applies to backend-specific
  *inputs* (the matching pre-check script, the matching nativeBuildInputs
  binaries). It does NOT apply to Go source changes in shared packages
  like `pkg/cache` — those invalidate every cohort because the cohorts
  share `packages.ncps`'s narrow fileset. That trade-off is the
  documented cost of the env-var cohort approach over the rejected
  build-tag-per-file split.

#### Scenario: Cold cache full check

- **WHEN** `nix flake check` runs on a cold cache
- **THEN** the unit-test and four backend-cohort derivations execute
  in parallel (subject to Nix's `--max-jobs` and `--cores` limits)
- **AND** no backend is started in more than one derivation at the
  same time on the same host beyond what its port allocation permits

### Requirement: Env-var presence drives integration-cohort membership

Membership in each backend-cohort derivation SHALL be determined by
the backend env vars exported in the derivation's `preCheck` (the
same env vars the dev-shell's `enable-<backend>-tests` helpers
export). Existing test code already gates per-backend subtests on
those env vars via `t.Skip`; this requirement codifies that the Nix
cohort layer reuses that mechanism rather than introducing a parallel
selection scheme (`go test -tags`, `-run` regex, etc.).

Build tags are NOT used for cohort selection because the codebase's
existing integration test files mix gated and ungated tests in the
same file (e.g. `pkg/cache/cache_test.go`,
`pkg/cache/cache_internal_test.go`,
`pkg/database/migrate/migrate_test.go`,
`pkg/ncps/migrate_narinfo_test.go`, `pkg/lock/redis/redis_test.go`),
so file-level tags would silently drop ungated tests from cohorts
that don't carry the tag.

#### Scenario: Adding a new test that needs PostgreSQL

- **WHEN** a developer adds a new test function that gates its body on
  `os.Getenv("NCPS_TEST_ADMIN_POSTGRES_URL")`
- **THEN** the test runs in the `ncps-postgres-tests` derivation
  (where the env var is exported and Postgres is up)
- **AND** the test skips at runtime in every other cohort (the env
  var is absent there)

#### Scenario: Adding a new test with no backend dependency

- **WHEN** a developer adds a new test function that does not check
  any backend env var
- **THEN** the test runs in every cohort (no skip path triggers)
- **AND** the test's wall-clock cost is paid once per cohort; this
  is accepted as the cost of not using build tags

### Requirement: Lint and drift checks reuse pre-built helper binaries

Checks that only invoke a small in-tree helper binary
(`cmd/ent-lint`, `cmd/atlas-sum-check`) SHALL consume that binary from
a single shared `packages.ncps-checktools` derivation (or equivalent
`passthru`) instead of each re-vendoring Go and re-running
`buildGoModule`. The check derivation itself MAY be `stdenvNoCC.mkDerivation`.

Checks that genuinely require a Go toolchain (`golangci-lint-check`,
`ent-codegen-drift-check`) MAY continue to use `buildGoModule`, but
MUST inherit `src` and `vendorHash` from `packages.ncps` so they share
cache hits with the main package and with each other.

#### Scenario: Running ent-lint-check

- **WHEN** `nix build .#checks.x86_64-linux.ent-lint-check` runs
- **THEN** the derivation depends on `packages.ncps-checktools`
- **AND** the derivation does NOT re-run `go mod download`,
  `go build ./cmd/ent-lint`, or `buildGoModule`
- **AND** the derivation completes by invoking the pre-built
  `ent-lint` binary against the schema tree

#### Scenario: Running golangci-lint-check shares cache with ncps

- **WHEN** `packages.ncps` has been built and cached
- **AND** `nix build .#checks.x86_64-linux.golangci-lint-check` runs
- **THEN** the Go module cache and vendor tree used by
  `golangci-lint-check` is the same fixed-output derivation as the
  one used by `packages.ncps`
- **AND** no second module fetch occurs

### Requirement: Main package build performs no tests

The `packages.ncps` derivation SHALL set `doCheck = false`. Test
execution lives in the new per-backend-cohort and unit-test
derivations. `nix build .#ncps` MUST produce the binary without
starting any backend service.

#### Scenario: Building the binary

- **WHEN** `nix build .#ncps` runs
- **THEN** no service backend (Garage, Postgres, MariaDB, Redis) is
  started
- **AND** no Go test runs as part of the build
- **AND** the resulting `result/bin/ncps` is a complete, runnable
  binary

#### Scenario: Build does not produce coverage

- **WHEN** `nix build .#ncps` runs
- **THEN** the derivation has a single output (`out`)
- **AND** no `coverage` output is produced

### Requirement: Coverage is produced by a dedicated derivation

A separate `packages.ncps.coverage` (or `packages.ncps-coverage`)
derivation SHALL produce a merged `coverage.txt` containing the union
of profiles from the unit-test and per-backend-cohort test
derivations. This derivation MUST NOT be a member of the `checks`
attribute set — `nix flake check` MUST NOT pay for coverage
instrumentation.

#### Scenario: CI builds coverage out-of-band

- **WHEN** the CI workflow invokes `nix build .#ncps.coverage`
- **THEN** the derivation runs the same test bodies as the
  per-backend-cohort check derivations, with `-coverprofile` enabled
- **AND** produces a single merged `coverage.txt` consumable by codecov

#### Scenario: `nix flake check` skips coverage

- **WHEN** `nix flake check` runs
- **THEN** the coverage derivation is not built
- **AND** no test runs with `-coverprofile`

### Requirement: The `checks` attrset is an explicit enumeration

The `checks` attribute set in `nix/checks/flake-module.nix` SHALL be
an explicit enumeration of quality-gate derivations. It MUST NOT
include `self'.packages` or `self'.devShells` via attribute-set
merge. A devShell or package may be a *dependency* of a check (built
implicitly), but MUST NOT be a check itself unless it asserts a
distinct quality property.

#### Scenario: Reading the checks attrset

- **WHEN** a maintainer reads `nix/checks/flake-module.nix`
- **THEN** each entry in the `checks` attrset has a comment naming the
  quality property it asserts
- **AND** there is no `// self'.packages` or `// self'.devShells`
  merge in the expression

### Requirement: Shared helper for database-backed checks

Spinning up backend services (Postgres, MySQL, Redis, Garage) for a
check SHALL go through a single Nix helper (`mkCohort`, or an
equivalent successor) that takes the list of required backends and
wires the appropriate `pre-check-*.sh` / `post-check-*.sh` scripts
plus a cleanup trap. Hand-rolled `preCheck` blocks that source the
per-backend helper scripts directly MUST NOT proliferate across check
derivations.

#### Scenario: Cohort derivations use the helper

- **WHEN** a maintainer reads the definitions of `ncps-postgres-tests`,
  `ncps-mysql-tests`, `ncps-redis-tests`, `ncps-s3-tests`,
  `ncps-unit-tests`
- **THEN** each is a single call to `mkCohort` with its `backends`
  list
- **AND** no derivation directly sources `pre-check-postgres.sh`,
  `pre-check-mysql.sh`, `pre-check-redis.sh`, or `pre-check-garage.sh`

#### Scenario: Adding a new backend-cohort check

- **WHEN** a new check that needs PostgreSQL is added
- **THEN** the check is defined as a call to `mkCohort` (or its
  successor) with `backends = [ "postgres" ]`
- **AND** no new copy of the Postgres preCheck wiring is introduced

#### Scenario: No standalone derivation duplicates cohort work

- **WHEN** a test gates its body on an `NCPS_TEST_*` env var
- **THEN** that test runs as part of the matching cohort
  (`ncps-<backend>-tests`)
- **AND** no separate check derivation is added that starts the same
  backend just to run that test — the cohort already covers it
  (see the Phase 4 removal of the standalone `schema-equivalence-check`
  for the precedent: `TestSchemaEquivalence`'s Postgres/MySQL paths
  are now covered by `ncps-postgres-tests` and `ncps-mysql-tests`
  respectively, and its SQLite path runs in every cohort)

### Requirement: Quality invariants preserved across the topology change

The restructured check graph MUST preserve every quality property of
the previous topology. Specifically:

- The race detector remains enabled for every test that had it before.
- Every integration backend (Garage, Postgres, MariaDB, Redis) is
  still exercised by at least one check derivation.
- The full set of `golangci-lint` linters and configured timeouts
  remains in effect (no narrowing).
- `ent-codegen-drift-check`, `atlas-sum-check`, `ent-lint-check`,
  and `helm-unittest-check` continue to fail the gate on the same
  inputs they fail on today.
- `TestSchemaEquivalence` (formerly run by the standalone
  `schema-equivalence-check` derivation, removed in Phase 4) continues
  to be exercised: its SQLite path runs in every cohort, its Postgres
  path runs in `ncps-postgres-tests`, and its MySQL path runs in
  `ncps-mysql-tests` — all via the same `t.Skip` env-var pattern the
  test already uses internally. No coverage lost; one fewer derivation.
- Codecov continues to receive a single merged coverage profile.

#### Scenario: Race detector remains on

- **WHEN** any unit-test or backend-cohort check derivation runs
- **THEN** the test invocation includes `-race`

#### Scenario: All backends still exercised

- **WHEN** `nix flake check` completes successfully
- **THEN** at least one derivation has started each of: Garage,
  PostgreSQL, MariaDB, Redis

#### Scenario: Lint coverage unchanged

- **WHEN** `golangci-lint-check` runs in the new topology
- **THEN** the set of enabled linters and the `--timeout` value
  match `.golangci.yml` as before the change

#### Scenario: Codecov receives one profile

- **WHEN** `nix build .#ncps.coverage` runs and CI uploads the result
- **THEN** codecov receives exactly one `coverage.txt` covering the
  same packages as before the change

### Requirement: Baseline and post-change timings are recorded

The change SHALL record per-derivation wall-clock timings on
CI-shaped hardware before and after each restructuring phase, in
files under `openspec/changes/lean-flake-check/`. Final wall-clock
SHALL be at least 40% lower than the recorded baseline on a cold
cache.

#### Scenario: Baseline captured before any change

- **WHEN** the first PR of this change lands
- **THEN** `openspec/changes/lean-flake-check/baseline-timings.txt`
  exists and contains per-derivation wall-clock figures for the
  current topology

#### Scenario: Final timings meet the wall-clock target

- **WHEN** all phases of this change are complete
- **THEN** `openspec/changes/lean-flake-check/final-timings.txt`
  exists
- **AND** the total wall-clock figure recorded there is at least 40%
  lower than the baseline figure on the same hardware class

### Requirement: Integration cohorts validated on a single CI architecture

Every workflow that validates the integration test suite (`nix flake check`,
i.e. the backend cohorts and coverage) — both the CI workflow (PRs/pushes) and
the release workflow (tag builds) — SHALL run it on exactly one canonical
architecture (`x86_64-linux`). Non-canonical architectures in the build matrix
(`aarch64-linux`) MUST NOT run `nix flake check` or build coverage; they MUST
still build their deployable OCI image so the multi-arch manifest remains
complete. This is configured by the caller passing
`test_systems: '["x86_64-linux"]'` to the shared `kalbasit/gh-actions` workflow
(both `ci.yml` and `releases.yml`).

This narrows where the suite runs, not what it covers: the cohorts, backends,
race detector, and the single merged Codecov profile are unchanged from the
`Quality invariants preserved across the topology change` requirement, because
those all derive from the canonical (x86_64) run.

#### Scenario: aarch64 CI leg skips the integration suite

- **WHEN** CI runs for a pull request with the default
  `systems: '["x86_64-linux","aarch64-linux"]'` and
  `test_systems: '["x86_64-linux"]'`
- **THEN** the `aarch64-linux` build leg SHALL NOT execute `nix flake check` or
  build/upload coverage
- **AND** the `aarch64-linux` leg SHALL still run
  `nix build .#packages.aarch64-linux.docker`

#### Scenario: x86_64 CI leg runs the full suite

- **WHEN** CI runs for the same pull request
- **THEN** the `x86_64-linux` build leg SHALL run `nix flake check -L` across all
  backend cohorts and produce the single merged coverage profile uploaded to
  Codecov

#### Scenario: Release tag build scopes the suite to x86_64

- **WHEN** a `v*.*.*` tag triggers the release workflow with
  `systems: '["x86_64-linux","aarch64-linux"]'` and
  `test_systems: '["x86_64-linux"]'`
- **THEN** the `x86_64-linux` leg SHALL run `nix flake check` + coverage and the
  `aarch64-linux` leg SHALL skip them
- **AND** both legs SHALL still build and push their OCI image, and the
  multi-arch manifest SHALL still be assembled

#### Scenario: Local flake check is unaffected

- **WHEN** a developer runs `nix flake check` locally on any system
- **THEN** all checks for that system SHALL run as before; the `test_systems`
  scoping is a CI-only workflow input and does not change flake behavior

### Requirement: Race+coverage test binaries are compiled once and shared

The flake SHALL compile the race- and coverage-instrumented test binaries
exactly once and share the result across all cohort derivations, rather than
recompiling per cohort. A dedicated derivation MUST produce a populated Go build
cache covering every distinct cohort compile invocation (the backend-cohort test
set and the cmd-cohort test set), and each cohort MUST consume that cache so its
`go test` invocation resolves compilation from cache instead of rebuilding.

The shared compilation MUST use the same Go toolchain, build flags
(`-race`, `-covermode=atomic`, and the per-cohort `-coverpkg` scope), and source
as the cohorts, so the cache is valid for them. A cache miss MUST degrade to a
correct (if slower) recompile, never to incorrect results.

This changes only where compilation happens; the backend-cohort granularity,
per-cohort `cover.out`, the single merged Codecov profile, the race detector,
and `t.Skip` env-var gating are all unchanged.

#### Scenario: A backend cohort reuses the shared cache

- **WHEN** the `ncps-postgres-tests` cohort builds
- **THEN** it SHALL seed `GOCACHE` from the shared compiled-cache derivation
  before running `go test`
- **AND** its `go test` SHALL resolve test-binary compilation from cache (no
  recompilation of the cgo/sqlite or race-instrumented objects)
- **AND** it SHALL still start only its own backend and emit its own `cover.out`

#### Scenario: Shared cache covers every cohort's compile

- **WHEN** the shared cache derivation builds
- **THEN** it SHALL compile (running zero tests) both the backend-cohort test set
  (`./pkg/... ./internal/... ./migrations/... ./testhelper/...`) and the
  cmd-cohort test set (`./cmd/... ./ent/... .`) with each set's `-coverpkg` scope
- **AND** every cohort — backend cohorts and the cmd cohort — SHALL find its
  compilation satisfied by that cache

#### Scenario: Quality invariants preserved

- **WHEN** the cohorts run with the shared cache
- **THEN** the race detector SHALL remain enabled, every backend SHALL still be
  exercised, and `nix build .#ncps.coverage` SHALL still upload exactly one
  merged coverage profile

### Requirement: CI runs each check as an independent parallel job

CI SHALL execute the flake's checks by fanning them out — running each
`checks.<system>.<name>` derivation as its own parallel job
(`nix build .#checks.<system>.<name>`) — rather than as a single monolithic
`nix flake check` on one runner. The set of jobs MUST be derived from the live
checks enumeration (`nix eval .#checks.<system> --apply builtins.attrNames --json`), so
adding or removing a check automatically adds or removes its job with no further
wiring. A single check's failure MUST NOT cancel the others (`fail-fast: false`),
and the CI gate MUST fail if any check job fails.

The checks run on the canonical CI architecture (`x86_64-linux`, per the
`Integration cohorts validated on a single CI architecture` requirement). Each
job relies on the shared build cache from Cachix so it pulls the compiled cohort
artifacts rather than rebuilding them.

#### Scenario: Each check is its own job

- **WHEN** CI runs for a pull request
- **THEN** for each attribute in `checks.x86_64-linux` there SHALL be a
  corresponding job running `nix build .#checks.x86_64-linux.<name>`
- **AND** the jobs SHALL run in parallel on separate runners
- **AND** one check failing SHALL NOT cancel the other check jobs, but SHALL fail
  the overall CI gate

#### Scenario: Check list is derived, not hand-maintained

- **WHEN** a check is added to or removed from the flake's `checks` attrset
- **THEN** its CI job SHALL appear or disappear automatically, because the matrix
  is computed from `nix eval .#checks.x86_64-linux --apply builtins.attrNames --json`

### Requirement: Coverage is produced by a job consuming the fanned-out cohorts

The single merged Codecov profile SHALL be produced by a dedicated CI job that
runs after the check jobs and builds `nix build .#ncps.coverage` (which merges
the per-cohort `cover.out` outputs), then uploads it to Codecov. Coverage upload
is informational and MUST NOT gate the build; a transient cache miss MAY cause a
local rebuild but MUST NOT fail CI.

#### Scenario: Coverage merged after checks

- **WHEN** the check jobs have completed
- **THEN** a coverage job SHALL build `.#ncps.coverage` and upload exactly one
  merged profile to Codecov
- **AND** a failure to upload coverage SHALL NOT fail the CI gate
