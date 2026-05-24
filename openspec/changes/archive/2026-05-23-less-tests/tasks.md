## 1. Profiling tooling and baseline

- [x] 1.1 Write `dev-scripts/profile-tests.py` that runs `go test -json -race -count=1 ./...` and emits two ranked tables: per-package wall time and per-test wall time (parent + subtests, `Elapsed > 500ms`). *(Implemented as Python to match existing `dev-scripts/` conventions. Supports `--packages`, `--threshold`, `--out`, `--from-file`, `--no-race`.)*
- [x] 1.2 Verify `profile-tests.py` works inside the Nix dev shell (smoke-tested on `./pkg/chunker/...` — 5 tests, 1.466s total, correctly ranked subtests). `nix flake check` integration deferred to task 8.1 end-to-end run.
- [x] 1.3 Run `profile-tests.py` against current branch tip with no test changes and save output to `openspec/changes/archive/2026-05-23-less-tests/baseline-timings.txt`. *(Unit-test-only baseline captured. Integration env vars not set — Postgres/MySQL/Redis tests show as skipped. Total wall time: **88.13s**.)*

  **Top-5 packages by wall time:**

  | rank | package | s | notes |
  |--:|---|--:|---|
  | 1 | `pkg/cache` | 16.5 | 226 tests, the suspected hotspot — but no single dominant slow test |
  | 2 | `pkg/cache/upstream` | 13.6 | **6 tests genuinely wait 5–12s on real timeouts** — biggest single-package win available |
  | 3 | `pkg/storage/s3` | 12.2 | **12+ ErrorPath tests at 3–5s each** — likely retry-with-backoff; investigate |
  | 4 | `pkg/lock/redis` | 7.8 | 13/15 skipped (no Redis); `TestNewLocker_ReturnType` is 6.8s — investigate |
  | 5 | `cmd/generate-migrations` | 5.1 | Two tests >4s each — likely shell out to `go run`/migration tooling |

  **Course correction vs. the original proposal:** `pkg/cache` is the #1 package but its 16.5s is spread across hundreds of tests. The two `TestCacheBackends` functions in `cache_test.go` (line 2786) and `cache_internal_test.go` (line 1528) are **not duplicates** — they share the parameterization shell (`for SQLite/PostgreSQL/MySQL`) but run **disjoint** sub-suites: cache_test.go covers external API (New, PutNarInfo, GetNar, etc.), cache_internal_test.go covers internals (RunLRU, MigrationDataIntegrity, WithReadLock, etc.). Keep both. The real wins are in `pkg/cache/upstream` and `pkg/storage/s3`.
- [ ] 1.4 Capture pre-change coverage baselines for the top-3 packages targeted by this change (`pkg/cache/upstream`, `pkg/storage/s3`, `pkg/cache`). Skip lower-ranked packages until they become in-scope.
- [ ] 1.5 Confirm baseline reproduces within 5% across two consecutive runs on the same machine; if not, document the noise floor in `baseline-timings.txt`.

## 2. pkg/cache: shared-fixture restructuring (reversible work first)

- [ ] 2.1 Inventory `pkg/cache/*_test.go`: list each `TestXxx`, its subtests, the fixtures it constructs (DB, storage, CDC chunk store, zstd encoders, NAR payloads), and whether subtests mutate them.
- [ ] 2.2 For each `TestXxx` where subtests construct identical immutable fixtures, hoist construction to the parent test. Derive per-subtest unique keys from `t.Name()` or a counter so `t.Parallel()` subtests don't collide.
- [ ] 2.3 Run `go test -race ./pkg/cache/...` after each hoist; if a race appears, revert that hoist and document why in this task.
- [ ] 2.4 Run coverage parity check for `pkg/cache` per spec `test-suite-efficiency#Coverage parity gate`. If fail, revert.
- [ ] 2.5 Re-profile `pkg/cache`; record wall-time delta in this task.

## 3. pkg/cache: table-driven consolidation

- [ ] 3.1 Identify clusters of single-case `TestXxx` functions that exercise the same function with different inputs (e.g., variants of `TestGetNarInfo_*`, `TestPutNarInfo_*`).
- [ ] 3.2 For each cluster, draft a single table-driven test with subtests, preserving every assertion from every input case. Do NOT delete the originals yet.
- [ ] 3.3 Run both old and new tests together; verify the new table-driven test fails on the same inputs as any deliberately-broken sanity probe.
- [ ] 3.4 Delete the original single-case tests. For each deletion, add a one-line justification to this task listing: deleted test name → surviving table-driven test name → case index.
- [ ] 3.5 Run coverage parity check for `pkg/cache`. If fail, revert.

## 4. pkg/cache: redundant test removal (four-gate rule)

- [ ] 4.1 For each remaining `TestXxx` in `pkg/cache`, capture per-test coverage profile via `go test -run '^TestXxx$' -coverprofile=...`.
- [ ] 4.2 Build a per-test coverage map; identify tests whose covered lines are a strict subset of another surviving test.
- [ ] 4.3 For each subset candidate, manually verify against the four-gate rule (spec `test-suite-efficiency#No two tests assert the same behavior on the same code path`): same path, same/weaker assertions, no unique fixture shape, no unique race exposure.
- [ ] 4.4 Delete tests that pass all four gates. Add a one-line justification per deletion to this task.
- [ ] 4.5 Run coverage parity check for `pkg/cache`. If fail, revert.
- [ ] 4.6 Re-profile `pkg/cache`; record total wall-time delta vs baseline in this task.

## 5. Apply the same loop to other slow packages

- [ ] 5.1 From the baseline ranking, pick the next slowest package (likely one of: `pkg/server`, `pkg/storage/local`, `pkg/storage/s3`, `pkg/database/*`, `pkg/ncps`). Repeat sections 2–4 scoped to that package.
- [ ] 5.2 Continue down the ranking until either (a) cumulative wall-time reduction reaches the ≥30% target or (b) remaining packages have no candidate removals after the four-gate rule.
- [ ] 5.3 Record per-package wall-time delta and removal count in this task.

## 6. Integration test split (per backend)

- [ ] 6.1 For Postgres integration tests: identify the wiring smoke test (connect → write → read → close). Confirm exactly one exists; if multiple, fold extras into table-driven subtests of the canonical one.
- [ ] 6.2 For Postgres integration tests asserting business logic against the real backend: identify those whose assertions could run against an in-memory fake. Move them. Backend-specific semantics (e.g., `ON CONFLICT`) stay as integration tests.
- [ ] 6.3 Repeat 6.1–6.2 for MySQL.
- [ ] 6.4 Repeat 6.1–6.2 for S3/MinIO. Backend-specific behavior (multipart, presigned URL semantics) stays as integration tests.
- [ ] 6.5 Repeat 6.1–6.2 for Redis. Distributed-locking semantics under contention stays as integration tests.
- [ ] 6.6 Run `nix flake check` end-to-end and verify all four backend wiring smoke tests still pass.
- [ ] 6.7 Run coverage parity check for each touched package. If any fail, revert that backend's batch.

## 7. Re-derive the -short gated set

- [ ] 7.1 Re-run `profile-tests.py` on the post-change tree. Save as `openspec/changes/archive/2026-05-23-less-tests/post-change-timings.txt`.
- [ ] 7.2 For each test newly above 500ms, add a `if testing.Short() { t.Skip(...) }` guard at the top.
- [ ] 7.3 For each currently-guarded test now below 500ms, remove the guard. Record each removal in this task with old/new timings.
- [ ] 7.4 Verify `go test -short -race ./...` completes in noticeably less time than `go test -race ./...` on the same machine; record both numbers in this task.

## 8. Verification and archive

- [ ] 8.1 Run `nix flake check` end-to-end. Capture total wall time. Compare against baseline. Record delta in this task.
- [ ] 8.2 Run `golangci-lint run --fix ./...`. Resolve any remaining lint issues by hand (especially `paralleltest`, `testpackage`, `testifylint`).
- [ ] 8.3 Run `nix fmt`.
- [ ] 8.4 Verify every test deletion in the diff has a corresponding one-line justification line in this `tasks.md` (spec `test-suite-efficiency#Per-removal justification in change tasks`).
- [ ] 8.5 Update `openspec/specs/short-test-mode/spec.md` with the synced delta (this is `/opsx:sync` work — done at archive time, not before).
- [ ] 8.6 Decide whether `dev-scripts/profile-tests.sh` stays as a project tool (recommended by design D6) or is removed. Land that decision in this task.
- [ ] 8.7 Run `/opsx:verify` and resolve any issues.
- [ ] 8.8 Run `/opsx:archive`.
