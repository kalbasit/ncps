## Why

`ncps fsck` aborts during phase 1g on production-sized caches with the PostgreSQL error
`extended protocol limited to 65535 parameters`. The CDC size-mismatch query eagerly loads
every `nar_file` with `total_chunks > 0` and asks Ent to fan-out load the related
`nar_info_nar_files` rows in a single `IN (...)` predicate. Once the cache exceeds ~65k
CDC nar_files, the follow-up `SELECT ... WHERE nar_file_id IN ($1...$N)` exhausts the
PostgreSQL extended-protocol parameter limit and the whole fsck run aborts before phase 2.

This is a hard blocker: there is no workaround for operators running PostgreSQL with a
cache that has grown past the threshold. fsck is the primary integrity tool, so this also
prevents detection of corruption on exactly the deployments that need it most (large,
long-lived caches).

## What Changes

- Replace the eager `.All(ctx)` + in-memory comparison in `queryCDCNarFilesWithSizeMismatch`
  with a streaming/paginated implementation that never holds more than a bounded number of
  rows in a single statement or in memory.
- Audit `pkg/ncps/fsck.go` (and any other Ent query in the fsck path) for similar
  unbounded `With*` eager-loads or `In(...)` predicates that can exceed the PostgreSQL
  65535-parameter ceiling, and convert them to bounded-batch iteration.
- Add a shared helper for batching `IN`-style queries with a safe default chunk size
  (well below 65535, accounting for per-row parameter multipliers) so future fsck phases
  do not regress.
- Add a regression test that exercises the size-mismatch path against a row count above
  the parameter ceiling (using the existing PostgreSQL test cohort) so the bug cannot
  silently return.

## Capabilities

### New Capabilities

_None — this is a correctness fix to existing fsck behavior._

### Modified Capabilities

- `fsck`: the size-mismatch detection phase MUST handle caches with arbitrarily many
  CDC `nar_files` without hitting database driver parameter limits. Behaviorally, fsck
  must complete phase 1 on PostgreSQL regardless of cache size.

## Non-goals

- No change to fsck's reporting format, CLI flags, or repair semantics.
- No change to the schema, migrations, or the meaning of `total_chunks` / `file_size` /
  `nar_size`.
- Not refactoring all of fsck to streaming — only the queries that demonstrably exceed
  driver parameter ceilings.
- Not switching ORMs or introducing raw SQL beyond what is necessary to bound parameter
  counts.

## Impact

- **Code**: `pkg/ncps/fsck.go` (specifically `queryCDCNarFilesWithSizeMismatch` and any
  sibling eager-load patterns); possibly a small helper in `pkg/database/` for batched
  IN-clause iteration.
- **Tests**: `pkg/ncps/fsck_test.go` gets a regression case sized above the parameter
  ceiling, gated on the existing PostgreSQL cohort env var.
- **Performance**: trades one large round-trip for several smaller ones. Expect slightly
  more network round-trips during phase 1g but bounded memory (no longer materializes
  every CDC nar_file plus its joined narinfo set at once). Net wall-clock impact on
  fsck should be neutral-to-positive on large caches because the current path simply
  fails.
- **APIs / dependencies**: none. No schema change, no new external dependency.
- **Operators**: unblocks fsck on existing production PostgreSQL deployments without
  any migration or config change.
