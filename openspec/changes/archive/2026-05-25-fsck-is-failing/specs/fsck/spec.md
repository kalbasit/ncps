## ADDED Requirements

### Requirement: Fsck CDC size-mismatch detection MUST scale beyond database driver parameter limits

The implementation of CDC size-mismatch detection (currently
`queryCDCNarFilesWithSizeMismatch`) SHALL NOT issue any single SQL statement whose
bound-parameter count grows unboundedly with the number of CDC `nar_file` rows or
their joined `narinfo_nar_files`/`narinfos` rows. In particular, it MUST NOT produce
an `IN ($1...$N)` predicate (whether emitted directly or via an ORM eager-load) where
N can exceed the PostgreSQL extended-protocol limit of 65535 parameters.

Detection SHALL iterate in bounded batches (chunk size chosen so that
`batch_size * params_per_row` stays well below 65535 across all supported drivers).
The fsck phase 1g run MUST complete successfully against caches of arbitrary size.

The behavioral contract of the existing "Fsck MUST detect CDC NARs whose stored size
mismatches the declared NarSize" requirement is unchanged: the same rows are flagged,
in the same category, with the same `totalIssues()` accounting. Only the execution
strategy changes.

#### Scenario: Phase 1g completes against a large CDC cache on PostgreSQL

- **WHEN** `ncps fsck` runs against a PostgreSQL database containing more CDC
  `nar_file` rows than the PostgreSQL extended-protocol parameter limit (65535)
- **THEN** phase 1g ("checking CDC NAR files with size mismatch") completes without
  returning `extended protocol limited to 65535 parameters`
- **AND** fsck proceeds to subsequent phases

#### Scenario: All size-mismatched rows are still detected when batching

- **WHEN** the cache contains a known set of size-mismatched CDC `nar_file` rows
  distributed across multiple internal batches
- **THEN** every size-mismatched row appears in `narFilesWithSizeMismatch`
- **AND** no correctly-sized CDC row is falsely flagged

#### Scenario: SQLite and MySQL paths remain correct

- **WHEN** `ncps fsck` runs the size-mismatch check against SQLite or MySQL
- **THEN** the batched implementation returns the same result set as the previous
  unbatched implementation for caches small enough that both would succeed

### Requirement: Fsck chunk-walk MUST scale beyond database driver parameter limits

The implementation that resolves the chunk rows linked to a single `nar_file` (currently
`chunksForNarFile`) SHALL NOT issue any single SQL statement whose bound-parameter
count grows unboundedly with the number of chunks belonging to that NAR. In particular,
it MUST NOT produce an `IN ($1...$M)` predicate on chunk IDs (whether emitted directly
or via an ORM eager-load) where M can exceed 65535.

The behavioral contract is unchanged: chunks are returned in `chunk_index` order, and
the returned slice length equals the link count.

#### Scenario: Resolving chunks for a NAR with > 65535 chunks succeeds on PostgreSQL

- **WHEN** `chunksForNarFile` is invoked for a `nar_file` whose `nar_file_chunks` row
  count exceeds the PostgreSQL extended-protocol parameter limit (65535)
- **THEN** the call returns without `extended protocol limited to 65535 parameters`
- **AND** the returned chunk slice contains every linked chunk in `chunk_index` order

### Requirement: Fsck MUST NOT introduce other unbounded IN-clause queries

Any future or existing fsck query that materializes a list of IDs (or composite keys)
and then performs a secondary `In(...)` / `IN (...)` lookup keyed on that list SHALL
use the same bounded-batch helper used by the CDC size-mismatch path, or an
equivalent streaming approach. New code in `pkg/ncps/fsck.go` MUST NOT regress to
unbounded `With*` eager-loads or unbounded `In(...)` predicates on collections that
can grow with cache size or with the size of any single NAR's chunk list.

#### Scenario: Code review surfaces unbounded eager-loads

- **WHEN** a new fsck phase is added that walks a table whose row count grows with
  cache size and joins to a related table
- **THEN** the implementation uses the shared bounded-batch helper (or documents why
  the join cardinality is bounded by an unrelated cap)
- **AND** does not call `.All(ctx)` on an unbounded parent query followed by an
  Ent `With*(...)` eager-load on the same query
