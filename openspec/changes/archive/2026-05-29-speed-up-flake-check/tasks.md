# Tasks: speed-up-flake-check

## 1. Implement the shared compiled cache

- [x] 1.1 In `nix/checks/flake-module.nix`, factored the `(coverpkg, paths)`
  invocation specs (`testSpecs`) and `compileFlags` into a single `let` binding
  shared by the cache build and the cohorts.
- [x] 1.2 Added the `_ncps-test-cache` derivation (override `_ncps-base`) running
  the compile-only (`-run '^$'`) form of both specs and copying `$GOCACHE` into
  `$out`. Required `disallowedReferences = [ ]` (the cache legitimately
  references the Go toolchain) and `dontFixup = true` (patchelf/strip would
  invalidate Go's content hashes). Exposed as `packages._ncps-test-cache`.
- [x] 1.3 `mkCohort` `preCheck` now seeds a writable `GOCACHE` from
  `_ncps-test-cache` for every cohort. Used `cp -r … && chmod -R u+w` (NOT
  `--no-preserve=mode`, which strips the `covdata` helper's +x bit and breaks
  coverage with "permission denied").
- [x] 1.4 Cohort `checkPhase` uses the shared `compileFlags`/`testSpecs` so the
  compile-affecting flags match the cache exactly (run-only flags differ).

## 2. Verify

- [x] 2.1 `nix build .#_ncps-test-cache` succeeds; produces an 821M cache.
- [~] 2.2 Backend cohort: `ncps-postgres-tests` fails locally on
  `TestSchemaEquivalence/postgres` — but this is ENVIRONMENTAL: it fails
  identically with my change stashed (old recompile path), and the same cohort
  PASSES in CI (run 26661047200, x86_64-linux success) on this branch. Not
  caused by this change.
- [x] 2.3 cmd cohort (`ncps-cmd-tests`) builds and PASSES (exit 0) with the
  shared cache. Cache reuse confirmed: the pre-fix run failed only on
  fork/exec of the *cached* `covdata` binary — proving compilation itself was
  served from cache, not recompiled.
- [~] 2.4 `nix build .#ncps.coverage` deferred to CI: it depends on every cohort
  (incl. the locally-flaky postgres one), so it can only be exercised cleanly in
  CI. No code path to the merge logic changed.
- [x] 2.5 `task fmt` clean (0 changed). No Go code touched, so `task lint`/`task
  test` are unaffected; `nix flake check` in CI is the real gate.
- [ ] 2.6 Record before/after x86 leg wall-clock once CI runs.

## 3. Stretch (optional)

- [ ] 3.1 If trivial, seed the same cache for `golangci-lint-check` (shares the
  source set). Skip if it complicates the derivation. (Deferred.)
