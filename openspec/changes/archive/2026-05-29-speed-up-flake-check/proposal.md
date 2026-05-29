# Proposal: speed-up-flake-check

## Why

After `speed-up-ci`, the `x86_64-linux` build leg (~17m) is CI's bottleneck,
dominated by `nix flake check`. dd9622c split flake-check into 5 cohort
derivations (8m→14m regression) because each cohort recompiles the
race+coverage-instrumented test binaries from scratch — Go's build cache is not
shared across Nix sandboxes. Profiling (cold cache, 8-core) measured the
backend-cohort test set at **~60s to compile** (cmd set ~62s), and it includes
cgo `mattn/go-sqlite3` under `-race`. The **4 backend cohorts compile
byte-identical binaries** (same paths, same `-coverpkg`); on CI's 4-core runners
that is ~3 redundant ~2-min compiles (~6m) that core contention can't
parallelize away.

## What Changes

- Add a shared derivation that compiles the race+coverage test binaries **once**
  and exposes a populated Go build cache as an output.
- The 5 cohort derivations consume that cache (`GOCACHE`), so each cohort's
  `go test` compile becomes a cache hit and the cohort is reduced to test
  execution + backend startup.
- No change to cohort membership, backend isolation, per-cohort `cover.out`,
  the merged Codecov profile, the race detector, or `t.Skip` env gating.

## Capabilities

### New Capabilities
- None.

### Modified Capabilities
- `flake-check-topology`: cohorts no longer each recompile; a single shared
  compilation feeds all cohorts. Adds a requirement that the
  race+coverage test binaries are compiled once and reused, and that the
  per-cohort behavior (backends, coverage, race) is otherwise unchanged.

## Impact

- **Affected:** `nix/checks/flake-module.nix` (cohorts + new shared cache
  derivation), possibly `nix/packages/flake-module.nix`. No Go production code
  changes; no `.github/workflows` changes; no migrations.
- **CI wall clock:** expected to recover most of dd9622c's ~6m compile
  regression on the x86 leg and relieve core contention so remaining cohort work
  parallelizes. Target: pull the x86 leg materially toward ~5m (floor = actual
  test execution + backend startup, measured in the design phase).
- **I/O / network / memory:** CI-only. Trades ~3 redundant compiles for a
  one-time cache build plus a per-cohort cache copy (bounded local IO, no
  network). No ncps runtime impact.

## Non-goals

- Collapsing the 5 cohorts back into one derivation (keeps dd9622c's localized
  per-backend failure attribution).
- Dropping the race detector or coverage, or changing which packages/backends
  are exercised.
- Touching the aarch64 scoping or any workflow YAML (owned by `speed-up-ci`).
