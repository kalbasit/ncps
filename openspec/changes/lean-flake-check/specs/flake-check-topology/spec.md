## ADDED Requirements

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

#### Scenario: Touching a Postgres-only integration test

- **WHEN** a developer changes a file tagged `//go:build integration_postgres`
  and runs `nix flake check`
- **THEN** Nix invalidates only the `ncps-postgres-tests` derivation
- **AND** only PostgreSQL is started during that derivation's `preCheck`
- **AND** the S3, MySQL, Redis, and unit-test derivations are served
  from cache

#### Scenario: Cold cache full check

- **WHEN** `nix flake check` runs on a cold cache
- **THEN** the unit-test and four backend-cohort derivations execute
  in parallel (subject to Nix's `--max-jobs` and `--cores` limits)
- **AND** no backend is started in more than one derivation at the
  same time on the same host beyond what its port allocation permits

### Requirement: Build-tag selection drives integration-cohort membership

Membership in each backend-cohort derivation SHALL be determined by Go
build tags on the test files (e.g., `//go:build integration_s3`,
`//go:build integration_postgres`, `//go:build integration_mysql`,
`//go:build integration_redis`). Selection MUST NOT be driven by
`go test -run` / `-skip` regex matches against test names, because
regex selection silently drifts when tests are renamed.

#### Scenario: Adding a new integration test

- **WHEN** a developer adds a new test that needs PostgreSQL
- **AND** the test file is tagged `//go:build integration_postgres`
- **THEN** the test runs only in the `ncps-postgres-tests` derivation
- **AND** the test does not run in the unit-test derivation

#### Scenario: Forgetting the build tag

- **WHEN** an integration test file lacks any `integration_*` build tag
- **THEN** a CI lint check MUST fail with a message naming the file
  and the missing tag
- **AND** the change MUST NOT merge until the tag is added

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
check SHALL go through a single Nix helper `mkDbBackedCheck`
(or equivalent) that takes the list of required backends and wires
the appropriate `pre-check-*.sh` / `post-check-*.sh` scripts plus a
cleanup trap. Hand-rolled `preCheck` blocks that source the per-backend
helper scripts directly MUST NOT proliferate across check derivations.

#### Scenario: schema-equivalence-check uses the helper

- **WHEN** a maintainer reads the definition of
  `schema-equivalence-check`
- **THEN** the derivation is a single call to `mkDbBackedCheck`
  with `backends = [ "postgres" "mysql" ]`
- **AND** the derivation does not directly source `pre-check-postgres.sh`
  or `pre-check-mysql.sh`

#### Scenario: Adding a new backend-cohort check

- **WHEN** a new check that needs PostgreSQL is added
- **THEN** the check is defined as a call to `mkDbBackedCheck`
  with `backends = [ "postgres" ]`
- **AND** no new copy of the Postgres preCheck wiring is introduced

### Requirement: Quality invariants preserved across the topology change

The restructured check graph MUST preserve every quality property of
the previous topology. Specifically:

- The race detector remains enabled for every test that had it before.
- Every integration backend (Garage, Postgres, MariaDB, Redis) is
  still exercised by at least one check derivation.
- The full set of `golangci-lint` linters and configured timeouts
  remains in effect (no narrowing).
- `ent-codegen-drift-check`, `atlas-sum-check`, `ent-lint-check`,
  `schema-equivalence-check`, and `helm-unittest-check` continue to
  fail the gate on the same inputs they fail on today.
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
