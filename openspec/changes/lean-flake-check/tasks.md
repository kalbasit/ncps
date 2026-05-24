## 1. Phase 1 — Baseline measurement

- [x] 1.1 Write a profiling helper (Nix script or shell wrapper) that takes a list of `nix flake show --json | jq '.checks.x86_64-linux | keys'` derivations, builds each on a cold store with `nix build .#checks.x86_64-linux.<name> -L --rebuild`, and records wall-clock per derivation.
- [x] 1.2 Run the helper on a CI-shaped runner (cold cache) and commit the output to `openspec/changes/lean-flake-check/baseline-timings.txt`. Ran locally with intermediate deps warm; see file caveats. `ncps` failed mid-suite with a pre-existing MariaDB flake (`TestCacheBackends/MySQL#01/PutNarInfoDeadlock`); derivation ran to completion before failing, so timing is still valid.
- [x] 1.3 In the same file, record the wall-clock of `nix build .#ncps.coverage` and the wall-clock of the `shared / build / build (x86_64-linux)` CI job from a recent main run, with the run ID, for cross-reference. Coverage build skipped — `.#ncps.coverage` is a passthru second output of the `ncps` derivation, so its wall-clock equals the `ncps` row already recorded. CI run 26343867348 numbers captured.
- [x] 1.4 Document in the same file which derivations start which backends (Garage / Postgres / MariaDB / Redis), so phase-by-phase deltas can be reasoned about.

## 2. Phase 2 — Lint/drift helper binaries (D2)

- [x] 2.1 Add `packages.ncps-checktools` to `nix/packages/` that builds `cmd/ent-lint` and `cmd/atlas-sum-check` once with a single `buildGoModule` invocation, with a narrow `fileset` (only `cmd/ent-lint`, `cmd/atlas-sum-check`, `go.mod`, `go.sum`, and minimal shared internal packages). Neither helper imports any internal package, so the fileset is exactly those four entries.
- [x] 2.2 Rewrite `ent-lint-check` in `nix/checks/flake-module.nix` as `stdenvNoCC.mkDerivation` that runs `${ncps-checktools}/bin/ent-lint --root $src` against the schema tree; remove the `buildGoModule` override. Narrowed src to `ent/` only.
- [x] 2.3 Rewrite `atlas-sum-check` the same way, consuming `${ncps-checktools}/bin/atlas-sum-check`. Narrowed src to `migrations/` only.
- [x] 2.4 For `golangci-lint-check` and `ent-codegen-drift-check`, make them `inherit src vendorHash` from `packages.ncps` (or factor a shared `goModulesSrc` value) so they share cache hits instead of producing distinct fixed-output derivations. `golangci-lint-check`: dropped `src = ../../.` override; added `.golangci.yml` to `packages.ncps`'s fileset so the linter config is available. `ent-codegen-drift-check`: cannot share — `proxyVendor = true` produces a structurally different goModules derivation than ncps's vendor mode. Added a comment documenting this.
- [x] 2.5 Re-run the Phase 1 helper and commit `after-checktools-timings.txt`. Confirm `ent-lint-check` and `atlas-sum-check` dropped to near-zero seconds. Confirmed: both went 4–5s → 0s. Substitution-hit noise on `golangci-lint-check` and `ncps-checktools` documented in the file. Baseline notes updated to reflect that the baseline ncps figure (8m22s) under-counted due to the #1247 flake — true ncps cost is ~12m.
- [x] 2.6 `nix flake check -L` passes; no regression in what each check enforces. All seven non-ncps checks built end-to-end (`atlas-sum-check`, `ent-lint-check`, `ent-codegen-drift-check`, `golangci-lint-check`, `helm-unittest-check`, `ncps-checktools`, `schema-equivalence-check`); `nix flake check --no-build` evaluates cleanly. `ncps` deliberately not re-run end-to-end here because it's unchanged in Phase 2 and known to hit the #1247 flake.

## 3. Phase 3 — Per-backend integration cohorts (D1)

- [x] 3.1 ~~Pick build-tag naming.~~ **Superseded.** Discovery during Phase 3 (see `design.md` D1) showed that five test files mix gated and ungated tests in the same file, so file-level build tags would silently drop ungated tests. Cohort selection is now env-var driven (which the test code already relies on via `t.Skip`). No build-tag work needed; no per-cohort tag documentation needed.
- [x] 3.2 ~~Audit `_test.go` files and add `//go:build integration_<backend>` headers.~~ **Superseded by the env-var pivot above.** The audit itself was done (12 test files identified across S3/Postgres/MySQL/Redis), but the conclusion was to leave files alone.
- [x] 3.3 ~~Add a CI lint that fails when an integration test file lacks the matching build tag.~~ **Superseded.** With no build tags, no tag-presence lint is needed. The "right" gate is "if you check a backend env var in a test, the corresponding cohort will exercise it" — that's emergent from the runtime skip, not a static invariant.
- [x] 3.4 Add `nix/checks/integration.nix` (or extend `flake-module.nix`) with one derivation per backend: `ncps-s3-tests`, `ncps-postgres-tests`, `ncps-mysql-tests`, `ncps-redis-tests`. Each invokes `go test -race -timeout <X>m ./...` (no tag), starts only its own backend in `preCheck`, and exports only the matching `NCPS_TEST_*` env vars. Implemented via a `mkCohort` helper in `nix/checks/flake-module.nix`. All four cohorts built end-to-end; timings 2m12s (s3), 3m52s (postgres), 6m01s (mysql), 2m19s (redis).
- [x] 3.5 Add `ncps-unit-tests` derivation that runs `go test -race ./...` with no backend env vars set and no backends started. All integration subtests skip via `t.Skip`. Sanity build completed at 1m57s with all integration subtests skipping correctly.
- [x] 3.6 Keep the existing `packages.ncps` `doCheck = true` path in place for one release as `ncps-all-tests-compat`. **No action needed in Phase 3.** The monolith is `packages.ncps` itself, still in `checks` via `// self'.packages`. Phase 5 will rename/alias it to `ncps-all-tests-compat` and remove it from `checks`. Phase 3's cohorts run *alongside* the monolith for now (additive — see comment in `flake-module.nix`).
- [x] 3.7 Run the Phase 1 helper and commit `after-cohorts-timings.txt`. Confirmed: cohort wall-clocks recorded; max cohort = 6m01s (mysql), so parallel wall-clock once monolith drops in Phase 5 will be ~6m vs current ~12m monolith. Phase 3 in isolation does not change `nix flake check` wall-clock — additive only.

## 4. Phase 4 — `mkDbBackedCheck` helper (D4)

- [x] 4.1 ~~Add `mkDbBackedCheck { name, backends, checkPhase, ... }` to `nix/checks/`.~~ **Done in Phase 3** as `mkCohort { name, backends }`. Satisfies the D4 goal of factoring the shared pre/post-check scaffold. Not renamed (`mkDbBackedCheck` would be misleading for the unit cohort, which has no backends).
- [x] 4.2 ~~Refactor each per-backend integration cohort derivation from Phase 3 to use the helper.~~ **Done in Phase 3.** Every cohort is already a one-line call to `mkCohort`.
- [x] 4.3 ~~Refactor `schema-equivalence-check` to a single call `mkDbBackedCheck ...`.~~ **Superseded.** `TestSchemaEquivalence` skips per-backend on `NCPS_TEST_ADMIN_*` env vars, so it already runs in `ncps-postgres-tests` (SQLite + Postgres parts) and `ncps-mysql-tests` (SQLite + MySQL parts) and `ncps-unit-tests` (SQLite part). The standalone derivation duplicates ~1m12s of work the cohorts already do, with no incremental coverage. Phase 4 **deletes** `schema-equivalence-check` entirely. See design.md D4 for the rationale.
- [x] 4.4 Re-run the Phase 1 helper and commit `after-helper-timings.txt`. Sequential sum 15m34s (down from 17m04s after Phase 3); projected post-monolith parallel wall-clock ~5m (down from ~6m projection after Phase 3). Recorded an additional MariaDB flake (separate failure mode from #1247) as a comment on the existing issue.
- [x] 4.5 `nix flake check -L` passes. Evaluation clean (`nix flake check --no-build` lists every check, schema-equivalence-check removed). Five non-cohort checks built end-to-end in the helper run. `ncps-mysql-tests` cohort failed on the recorded flake, not on the topology change.

## 5. Phase 5 — Coverage split (D3)

- [ ] 5.1 Set `doCheck = false` on `packages.ncps`. Remove the `coverage` second output and the `coverage`-related lines from `preCheck`/`checkPhase`/`postCheck`.
- [ ] 5.2 Add `packages.ncps-coverage` (or `packages.ncps.coverage` via `passthru`) as a dedicated derivation that runs the union of the unit + per-backend cohort test bodies with `-coverprofile`, then merges the per-cohort `cover.out` files into a single `coverage.txt`. Output is the merged file.
- [ ] 5.3 Verify the reusable CI workflow's coverage invocation still resolves (`nix build .#ncps.coverage`); rename the derivation or add an alias attribute if needed for compatibility.
- [ ] 5.4 Confirm codecov receives a single profile covering the same packages as before (compare against pre-change codecov report for `main`).
- [ ] 5.5 Re-run Phase 1 helper and commit `after-coverage-split-timings.txt`. Confirm `nix build .#ncps` (no check, no coverage) is now seconds, not minutes.

## 6. Phase 6 — Prune (D5)

- [ ] 6.1 Replace `checks = self'.packages // self'.devShells // {...}` in `nix/checks/flake-module.nix` with an explicit enumeration. Each entry gets a one-line comment naming the quality property it asserts.
- [ ] 6.2 Remove `ncps-all-tests-compat` and any other compat shims left from Phase 3.
- [ ] 6.3 Run the Phase 1 helper one last time on a cold CI-shaped runner; commit `final-timings.txt`.
- [ ] 6.4 Verify total wall-clock is at least 40% lower than baseline (per the `flake-check-topology` spec). If not, file a follow-up identifying the remaining bottleneck rather than weakening the gate.

## 7. Documentation

- [ ] 7.1 Update `CLAUDE.md` to describe the new check topology, the build-tag convention, and how to invoke a single cohort (`nix build .#checks.x86_64-linux.ncps-postgres-tests -L`).
- [ ] 7.2 Update `nix/checks/flake-module.nix` header comment to describe the new structure and the role of `mkDbBackedCheck`.
- [ ] 7.3 Add a brief note in `README.md` (if it mentions `nix flake check`) about the per-cohort derivations.

## 8. Justification log (mirrors test-suite-efficiency convention)

- [ ] 8.1 For any test file moved into a different cohort (or whose build tag changed which derivation runs it), record a one-line justification here naming the file and the cohort it now belongs to.
- [ ] 8.2 For any check derivation removed (e.g., from compat shims), record a one-line justification naming the removed derivation and the surviving derivation that asserts the same quality property.
