## Context

Two queries in `pkg/ncps/fsck.go` issue Ent eager-loads (`With*`) on top of unbounded
parent rowsets. Both compile to `SELECT ... WHERE <fk> IN ($1, ..., $N)` follow-ups, and
PostgreSQL's extended protocol caps bound parameters at 65535 per statement.

**Site 1 — `queryCDCNarFilesWithSizeMismatch` (`pkg/ncps/fsck.go:782`)** — confirmed
broken in production:

```go
dbClient.Ent().NarFile.Query().
    Where(entnarfile.TotalChunksGT(0)).
    WithNarInfoNarFiles(func(q *ent.NarInfoNarFileQuery) {
        q.WithNarinfo()
    }).
    All(ctx)
```

The outer `WithNarInfoNarFiles` emits a `SELECT FROM narinfo_nar_files WHERE
nar_file_id IN ($1...$N)` where N grows with the number of CDC `nar_file` rows. On the
failing production cache (~82.5k `nar_file` rows, the great majority CDC), N exceeds
65535 and the driver returns `extended protocol limited to 65535 parameters`; fsck
aborts phase 1 collection. SQLite and MySQL paths happen to tolerate the current sizes
but the same pattern would break them at higher cardinalities.

**Site 2 — `chunksForNarFile` (`pkg/ncps/fsck.go:1661`)** — same shape, narrower
trigger:

```go
dbClient.Ent().NarFileChunk.Query().
    Where(entnarfilechunk.NarFileIDEQ(narFileID)).
    Order(ent.Asc(entnarfilechunk.FieldChunkIndex)).
    WithChunk().
    All(ctx)
```

Here the parent is bounded to a single NAR's chunk links, but the `WithChunk()` eager
load emits `SELECT FROM chunks WHERE id IN ($1...$M)` where M is the link count for
that NAR. A single multi-gigabyte CDC NAR can exceed 65535 chunks at typical chunk
sizes, hitting the same driver cap. Less likely to fire than Site 1 in current
deployments, but the failure mode and fix are identical, so we batch both in one PR
rather than file a follow-up that will sit until someone uploads a 60+ GB closure.

## Goals / Non-Goals

**Goals**

- Make phase 1g complete on PostgreSQL caches of arbitrary size.
- Keep the behavioral contract of size-mismatch detection unchanged (same rows flagged,
  same `fsckResults.narFilesWithSizeMismatch`).
- Fix `chunksForNarFile` so a single oversized CDC NAR (>65535 chunks) cannot break
  fsck on PostgreSQL via the `WithChunk()` eager-load.
- Provide a reusable helper so future fsck additions cannot regress into the same trap.
- Add PostgreSQL-cohort regression tests that exercise row counts above the parameter
  ceiling for both sites.

**Non-Goals**

- Rewriting fsck's structure or splitting it into a streaming pipeline.
- Changing schema, migrations, or what counts as a "mismatch".
- Optimizing fsck wall-clock time beyond what the bug fix incidentally improves.
- Switching ORMs or moving to raw SQL anywhere it isn't strictly required to bound
  parameter counts.
- Auditing the entire codebase outside `pkg/ncps/fsck.go` — this change is scoped to
  fsck.

## Decisions

### Decision: Bound the parent query, drop eager-load, do per-batch lookups

Replace the single `.All(ctx)` + `With*` eager-load with a paginated walk. The shape
applies to both sites:

**`queryCDCNarFilesWithSizeMismatch`** (parent grows with cache size):

1. Iterate CDC `nar_file` rows in ID-ordered pages: `Where(TotalChunksGT(0), IDGT(last))`
   `Order(Asc(ID))` `Limit(batchSize)` `All(ctx)`. Keyset pagination (vs `Offset`) keeps
   each round-trip O(batchSize) on the index and is safe under concurrent writes.
2. For each page, collect the `nar_file` IDs and run a separate query against
   `narinfo_nar_files` joined to `narinfos`, filtered with `NarFileIDIn(ids...)` and
   `WithNarinfo()`. The page is bounded, so this secondary `IN` is bounded.
3. Compare `nar_file.file_size` against each linked `narinfo.nar_size` in memory,
   exactly as today.
4. Continue until a short page (fewer than `batchSize` rows) is returned.

**`chunksForNarFile`** (parent grows with chunk count of one NAR):

1. Iterate `nar_file_chunks` rows in `chunk_index`-ordered pages:
   `Where(NarFileIDEQ(narFileID), ChunkIndexGT(last))`
   `Order(Asc(ChunkIndex))` `Limit(batchSize)` `All(ctx)`. Keyset pagination on
   `chunk_index` preserves the existing ordering contract (chunks returned in index
   order) without `Offset`.
2. For each page, collect chunk IDs and fetch chunks with
   `Chunk.Query().Where(IDIn(ids...)).All(ctx)`, then re-order locally to match the
   link order from step 1 before appending. Doing the fetch separately (rather than
   `WithChunk()` per page) avoids relying on Ent's edge-load preserving page order.
3. Continue until a short page is returned.

**Why this shape and not "pure SQL join with WHERE file_size != nar_size"**

- A direct join expression would be cleanest but the current code chose Ent + in-memory
  comparison precisely because Ent's fluent API does not express cross-table column
  comparison and the project's convention is to stay on Ent (no hand-written SQL except
  via sqlc generators that don't cover this query). Staying on Ent keeps the fix narrow.
- The same problem would still need bounded batching for the eager-load even if we
  filtered server-side, because `WithNarinfo()` itself drives the parameter explosion.

### Decision: Batch size of 1000 rows for both sites

Per-page worst-case parameter usage:

- **Size-mismatch path**: page IDs (1 param each) feed the secondary
  `NarFileIDIn(...)` (`batchSize` params), and `WithNarinfo()` issues another `IN` over
  linked `narinfo_id`s (typically ~1 per CDC nar_file, possibly more if a NAR is
  shared). Peak is on the order of `2 * batchSize`.
- **chunksForNarFile path**: page IDs feed `IDIn(...)` once per page — peak is
  `batchSize` params.

At `batchSize = 1000`, peak parameter count stays around 1000–3000 — two orders of
magnitude under the PostgreSQL 65535 ceiling, with headroom for sharing fan-out. Use a
single shared constant `fsckEagerLoadBatchSize = 1000` for both sites, documented
inline with the PostgreSQL 65535 cap as the reason. One knob is easier to tune than
two and there is no reason for them to diverge.

### Decision: Shared `batchIDs` helper in `pkg/ncps/fsck.go`

Add an unexported helper:

```go
// batchedIDIter walks rows of T from an Ent query in keyset-paginated batches of
// `batchSize`, invoking `fn` per batch. It exists to keep secondary IN-clause
// queries from exceeding driver parameter limits (notably PostgreSQL's 65535
// extended-protocol cap).
func batchedIDIter[T any](
    ctx context.Context,
    batchSize int,
    fetch func(ctx context.Context, afterID int, limit int) ([]T, int, error),
    fn func(ctx context.Context, batch []T) error,
) error
```

Scope it to the fsck package for now — promoting to `pkg/database` is a follow-up if
another caller appears. This keeps the blast radius minimal and avoids prematurely
designing a generic abstraction.

### Decision: Regression tests gated on the PostgreSQL cohort

Add two tests to `pkg/ncps/fsck_test.go`, both gated on `NCPS_TEST_POSTGRES`:

- `TestQueryCDCNarFilesWithSizeMismatch_LargePostgreSQL` — seed `> 70_000` CDC
  `nar_file` rows linked to narinfos via `narinfo_nar_files`, with a small known
  subset whose `file_size` differs from the linked `narinfo.nar_size`. Assert the
  function returns without error and that exactly the seeded mismatched IDs are
  returned.
- `TestChunksForNarFile_LargePostgreSQL` — seed one `nar_file` with `> 70_000`
  `nar_file_chunks` rows pointing at distinct `chunks` rows. Assert the function
  returns without error, returns the full chunk list in `chunk_index` order, and
  length matches the seeded count.

Seed performance is a one-time cost; use single `INSERT ... SELECT` statements per
table to keep each test under a few seconds.

We do NOT add the same volumetric tests for SQLite/MySQL — those drivers do not have
the same hard cap and seeding 70k rows in SQLite is needlessly slow. The smaller
existing fsck tests already cover correctness across all three engines.

### Decision: No new metric, no new flag

The fix is correctness-only. We do not expose batch size as a CLI flag and do not add a
metric for "batches processed". If the constant proves wrong for some deployment, a
follow-up change can promote it; YAGNI for now.

## Risks / Trade-offs

- **More round-trips**: phase 1g goes from 1 query (when it works) to roughly
  `ceil(N / 1000) * 2` queries. On the failing PostgreSQL cache that's ~166 round-trips
  vs the current zero (because the current path aborts). Net wall-clock impact is
  strongly positive in the failure mode. On smaller caches that previously succeeded,
  the added latency is bounded — typically tens of milliseconds. `chunksForNarFile`
  goes from 1 query to `ceil(chunkCount / 1000) * 2` queries — negligible for typical
  NARs (one or two batches) and only meaningful for very large CDC NARs where it was
  about to fail outright anyway.
- **Keyset pagination assumption**: relies on `nar_file.id` being monotonically
  increasing and indexed. Both hold (it is the primary key). Concurrent inserts during
  fsck can cause new rows to appear in later pages; that is acceptable — fsck has
  always been a best-effort snapshot.
- **In-memory comparison still O(rows linked)**: total memory is bounded by `batchSize`
  worth of `nar_file` + their linked `nar_info_nar_files` and `narinfo` rows. Drops
  peak memory significantly vs the current `.All(ctx)` that materializes everything.
- **Two queries per batch**: an alternative is one query per batch (a real JOIN against
  `narinfo_nar_files` + `narinfos`). We keep two for now because Ent's eager-load with
  bounded `IN` is the smallest diff from the existing code. If profiling shows the
  extra round-trip matters, collapse to a single Ent query that joins explicitly.

## Migration Plan

Pure code change. No schema change, no data migration, no flag change. Single PR:

1. Implement the batched helper + the refactor of `queryCDCNarFilesWithSizeMismatch`.
2. Add the large-cache PostgreSQL regression test (cohort-gated).
3. Run `nix flake check` (specifically `ncps-postgres-tests`) to validate.
4. Operators upgrade and re-run `ncps fsck`; phase 1g now completes.

No rollback procedure needed — reverting the commit restores the prior (broken on large
PostgreSQL caches) behavior.

## Open Questions

- Should the helper move to `pkg/database/` immediately? Deferred — promote on second
  caller, not on speculation.
- Should we add a generic eager-load lint to `cmd/ent-lint`? Out of scope for this
  change; tracked separately if it becomes a recurring footgun.
