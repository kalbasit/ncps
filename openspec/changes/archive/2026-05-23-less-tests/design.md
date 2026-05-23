## Context

`nix flake check` runs `go test -race ./...` plus all dependent checks. The Go test step dominates wall time, with `pkg/cache` suspected as the worst offender. The suite has grown organically: many `TestXxx` functions independently rebuild expensive fixtures (SQLite/Postgres/MySQL connections, MinIO buckets, fresh CDC chunk stores, zstd pools, full NarInfo+NAR roundtrips with random payloads), and several tests assert overlapping behavior on the same code paths.

The project already has a `short-test-mode` capability that gates >500ms tests behind `testing.Short()`. That is orthogonal to this work — `-short` lets developers skip slow tests locally, but `nix flake check` (and CI) still runs everything. Reducing the *total* test time requires removing genuine redundancy and amortizing setup, not skipping more tests.

Constraints:
- Race detector must remain on for every test.
- `paralleltest` linter requires `t.Parallel()` everywhere; restructuring must not break that.
- `testpackage` linter requires `_test` package; no internal access via package-private symbols.
- Integration tests are gated by env vars (`enable-*-tests`) but run under `nix flake check`; they are in scope.
- No production code changes; this is a test-suite-only effort.

## Goals / Non-Goals

**Goals:**
- Measure first: produce a reproducible profile of per-package and per-test wall time before any deletion.
- Establish a written decision procedure for "is this test redundant?" that future reviewers can apply.
- Reduce `go test -race ./...` wall time on a clean machine by a meaningful margin (target: ≥30% reduction; stretch: ≥50%) without losing coverage signal.
- Keep coverage parity: line coverage MUST NOT drop; branch coverage MUST NOT drop by more than 1pp per package.
- Leave the suite easier to extend — shared fixtures named clearly, table-driven where natural.

**Non-Goals:**
- Adding new tests or new coverage.
- Touching production code under test (except trivial test-only `helpers` files moved/renamed).
- Replacing real-backend integration tests with mocks wholesale. We keep at least one wiring smoke test per backend (Postgres, MySQL, S3/MinIO, Redis).
- Removing or weakening the race detector, parallelism, or `nix flake check` structure.
- Changing the `-short` gating set as a primary goal. Updates to `short-test-mode` are downstream of profiling.

## Decisions

### D1. Profiling methodology: `go test -json` + a small analyzer

Use `go test -json -race -count=1 ./...` (with integration env vars set via `enable-integration-tests`) piped to a temporary analyzer that ranks:
- Per-package total wall time
- Per-test wall time (parent + subtests separately)
- Tests where `Elapsed > 500ms`

**Why this over `gotestsum`**: We already have everything we need in `-json`. A small Python script is cheaper than adding a tool dependency and is reproducible across local + CI. The output is stored as `openspec/changes/less-tests/baseline-timings.txt` (not committed past archive) for the duration of the work.

**Alternative considered**: CPU profiling per test with `-cpuprofile`. Rejected for this phase — wall-time ranking is enough to find the offenders; CPU profiles add noise without changing the priority list.

### D2. "Redundant test" decision procedure

A test (or subtest) is a removal candidate **only if all four** hold:

1. **Same code path**: Its per-test coverage profile (`go test -coverpkg=./... -coverprofile=...`) is a subset of another test's profile.
2. **Same assertions or weaker**: Every property it asserts is asserted by at least one other test that survives, with equal or stronger specificity (e.g., a test that checks `err != nil` is dominated by one that checks `errors.Is(err, ErrXxx)`).
3. **No unique fixture shape**: It does not exercise a unique input boundary (empty input, max-size input, unicode, concurrent access, specific backend quirk).
4. **No unique race-detector exposure**: It does not exercise a goroutine interleaving that no other test exercises (checked by reading, not by tooling — race coverage is not machine-measurable).

If any one of those fails, the test stays. Each removed test gets a one-line justification in `tasks.md` referencing the surviving test that covers it.

**Why this over "delete anything that looks similar"**: Tests sometimes look duplicative but cover a subtle path (e.g., the third PutNarInfo in a row hits a different cache state). The four-gate rule forces an explicit check before deletion.

### D3. Shared-fixture restructuring rules

Within a single `TestXxx`:
- Hoist setup that does not mutate shared state out of subtests into the parent test (DB schema creation, MinIO bucket creation, zstd encoder pool, fixed-content NAR fixtures).
- Per-subtest, derive unique keys/paths from `t.Name()` (already sanitized) or a counter so `t.Parallel()` subtests do not collide.
- Use `t.Cleanup` once at the parent level for shared resources; subtest-local cleanup stays per subtest.
- Never share a `*sql.DB` connection across `TestXxx` boundaries unless an existing helper already does so safely — keep package-level fixtures opt-in, not automatic.

Across `TestXxx` boundaries, package-level setup is allowed only when:
- The fixture is read-only (e.g., a parsed NAR file used as input).
- A `TestMain` already exists or is trivially added without breaking parallelism.

**Why not aggressive package-level fixtures everywhere**: package-level shared state interacts badly with `t.Parallel()` and is a common source of flaky tests under `-race`. We allow it only for read-only data.

### D4. Integration test split

For each backend (Postgres, MySQL, S3/MinIO, Redis):
- Keep one "wiring smoke" test that exercises connect → simple roundtrip → close against the real backend.
- Move business-logic assertions (error handling branches, retry behavior, edge cases) to unit tests against a fake implementing the storage/database interface, when such a fake already exists or is trivial to write.
- Tests that genuinely need backend-specific behavior (e.g., Postgres-specific upsert semantics, S3 multipart) stay as integration tests.

**Why**: Real-backend startup (process-compose dependencies) plus per-test schema migration is the dominant cost in many integration test files. Wiring smoke tests catch integration regressions; business logic does not need the real backend to assert correctness.

### D5. Coverage parity gate

Enforced in `tasks.md` as a checkpoint after every batch of removals/restructuring:

```
go test -coverpkg=./... -coverprofile=cover-after.out -race ./<changed-pkg>/...
```

Compare against `cover-before.out` recorded at the start. Requirements:
- Line coverage per package: `after >= before`.
- Branch coverage per package: `after >= before - 1pp` (tolerance for inlining/optimization artifacts).
- Any regression beyond the tolerance reverts the batch.

**Why a per-batch gate instead of one final gate**: catching a coverage drop after every batch lets us bisect the offending removal in seconds, not hours.

### D6. Sequencing

1. Land the profiling tooling and baseline numbers as the first commit (no test changes yet).
2. Work package-by-package, starting with the slowest package (most likely `pkg/cache`).
3. Within each package, work file-by-file, smallest restructuring first (shared setup), then table-driven consolidation, then deletions.
4. After each package, re-run the profiler and update `tasks.md` with measured wall-time delta.
5. After all packages, update `short-test-mode` spec to reflect the new gated set.

**Why this order**: shared-setup restructuring is reversible and low-risk; deletions are not. Doing the safe work first builds confidence and surfaces unique-coverage signals (a test that "looked redundant" but breaks when its setup is shared is doing real work).

## Risks / Trade-offs

- **Risk: a deleted test was actually catching a regression no other test catches.**
  → Mitigation: D2's four-gate rule, the per-batch coverage gate (D5), and a written justification per removal in `tasks.md`. The git history of `tasks.md` becomes the audit trail.

- **Risk: shared fixtures introduce hidden coupling and make a future test flaky under `-race`.**
  → Mitigation: D3 limits package-level sharing to read-only data; subtest-level sharing requires per-subtest keyspaces. CI runs with `-race` and would catch a regression.

- **Risk: integration tests moved to fakes diverge from real backend behavior.**
  → Mitigation: D4 requires keeping one wiring smoke test per backend. Backend-specific semantics (upsert, multipart) stay as integration tests.

- **Risk: the profiler analyzer itself becomes a maintenance burden.**
  → Mitigation: Keep it as a shell pipeline in `dev-scripts/`, not a new Go program. Delete it (or move to `dev-scripts/profile-tests.sh`) once the change is archived. Document in the spec.

- **Trade-off: per-batch coverage gating slows the work down.**
  → Accepted. The cost of one careless deletion is far higher than a few extra `go test -cover` runs.

- **Risk: `nix flake check` cache invalidation patterns change after restructuring.**
  → Mitigation: the change is test-only; `nix flake check`'s overall structure (jobs, dependencies) is unchanged. Verified by running `nix flake check` before and after.

## Open Questions

- Should the profiling pipeline be checked into `dev-scripts/` permanently as a project tool, or kept only for the duration of this change? **Tentative answer**: check it in — future test-suite drift is inevitable and the tooling is cheap.
- What is the right target reduction? **Tentative answer**: ≥30% with stretch ≥50%, validated against the baseline. Revisit after first profiling pass; if the suite is already lean we adjust the target down rather than force deletions.
- Do we want to add a CI job that prints test timings as a summary to catch future regressions? Out of scope for this change but worth a follow-up.
