# flake-check-topology (delta: speed-up-flake-check)

## ADDED Requirements

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
