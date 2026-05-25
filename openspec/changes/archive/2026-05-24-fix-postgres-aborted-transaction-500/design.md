## Context

The `chunks` table has an auto-increment integer PK (`id`) and a separate
`UNIQUE` index on `hash`. `recordChunkBatch` inserts chunks via an upsert:
`ON CONFLICT (hash) DO UPDATE SET updated_at = now()`. This is intended to
return the chunk's `id` (via `RETURNING id`) regardless of whether the row
was newly inserted or already existed.

The bug: when the PostgreSQL sequence for `chunks_id_seq` is desynced with
the table data (e.g., after a database restore or manual data import), the
`INSERT` fails on `chunks_pkey` (auto-increment PK) before PostgreSQL can
evaluate the `ON CONFLICT (hash)` clause. This is a plain unique_violation
(SQLSTATE 23505) on the PK, which `withEntTransactionRetry` treats as
retryable — so it retries 5 times, each time consuming a new desynced
sequence value, each time failing again.

After all 5 retries fail, the transaction is rolled back correctly by
`WithTransaction`. However, if any intermediate state in the same request
shares the connection (via an outer transaction or connection reuse in the
request path), a subsequent query sees PostgreSQL's aborted-transaction state
(SQLSTATE 25P02) and fails. ncps surfaces this as HTTP 500.

## Goals / Non-Goals

**Goals:**
- Eliminate `chunks_pkey` duplicate key failures during CDC chunk recording
- Prevent HTTP 500 from 25P02 leaking to narinfo or NAR responses
- Keep the fix minimal: no schema changes, no sequence-reset migrations

**Non-Goals:**
- Fixing the underlying sequence desync (operational concern, not a code bug)
- Changing the retry policy for other operations
- Altering the `nar_file_chunks` bulk-insert path (already uses `DO NOTHING`)

## Decisions

### Decision 1: Replace per-chunk upsert with DO NOTHING + SELECT

**Chosen**: In `recordChunkBatch`, change each per-chunk upsert from:
```
ON CONFLICT (hash) DO UPDATE SET updated_at = now()
```
to:
```
ON CONFLICT (hash) DO NOTHING
```
Then immediately query `WHERE hash = <hash>` to retrieve the chunk's `id`.

**Rationale**: Chunks are content-addressed immutable blobs. There is no
semantic reason to touch `updated_at` on an existing chunk — it's already
stored correctly. Using `DO NOTHING` means the INSERT never touches the PK
path at all if the chunk exists, avoiding the sequence desync problem
entirely. The extra `SELECT WHERE hash = ...` per unique chunk is a
lightweight indexed lookup and only fires when the chunk pre-exists.

**Alternative considered**: Fix the sequence desync operationally (ALTER
SEQUENCE ... RESTART). Rejected as the sole fix: requires manual
intervention per deployment and doesn't prevent recurrence if the sequence
drifts again. The code-side DO NOTHING fix is resilient to future drift.

**Alternative considered**: Keep `DO UPDATE` but also handle PK conflict
separately. Rejected: more complex than needed, still hits the sequence.

**Alternative considered**: Collapse chunk insert + ID retrieval into a
single `INSERT ... ON CONFLICT (hash) DO NOTHING RETURNING id` and handle
the case where RETURNING returns 0 rows (i.e., chunk pre-existed). This
requires raw SQL or a custom Ent hook; the two-step approach (INSERT +
SELECT) is clearer and fully supported by Ent's query builder.

### Decision 2: Add 25P02 detection and connection rollback

**Chosen**: In `withEntTransactionRetry`, after all retries are exhausted
(or when a non-retryable error occurs), detect SQLSTATE 25P02
("in_failed_sql_transaction") and issue an explicit `ROLLBACK` on the
current database connection before returning the error. This ensures the
connection is returned to the pool in a clean state.

The detection point is the error returned from `dbClient.WithTransaction`.
If the error (or its cause) carries SQLSTATE 25P02, call `db.ExecContext`
with `ROLLBACK` on the raw connection before returning.

**Alternative considered**: Configure `pgxpool` with a `BeforeAcquire` hook
that checks for 25P02 and rolls back before handing the connection to the
caller. Rejected: ncps uses `database/sql` (not pgxpool directly), so there
is no `BeforeAcquire` hook available without significant refactoring.

**Alternative considered**: Set `SetMaxConnLifetime` to a short value so
dirty connections are recycled quickly. Rejected: this masks the bug rather
than fixing it, and adds unnecessary connection churn under load.

**Alternative considered**: Wrap every non-transactional DB query with
25P02 detection and auto-retry on a fresh connection. Rejected: too broad;
masks the real problem and adds complexity to every query site.

## Risks / Trade-offs

**[Risk] Extra SELECT per pre-existing chunk** → One indexed lookup per
chunk that already exists in the DB. During normal operation (first-time
chunking), no extra SELECTs. During retry or duplicate chunking (rare),
one SELECT per chunk. Acceptable overhead.

**[Risk] DO NOTHING on hash returns no RETURNING row — Ent ID() fails** →
Addressed by using `Exec(ctx)` (not `ID(ctx)`) for the INSERT, then a
separate `Query().Where(hash).Only(ctx)` to fetch the ID.

**[Risk] Sequence desync persists** → The sequence will still be desynced
and WILL affect future first-time inserts (brand new chunks). Those will
fail the INSERT on `chunks_pkey` too. But with DO NOTHING, this only applies
to truly new chunks; pre-existing chunks are unaffected. A runbook note
should explain how to reset the sequence (`SELECT setval('chunks_id_seq',
MAX(id)) FROM chunks`). The code fix prevents cascading failures; the
operational fix resolves drift permanently.

**[Risk] 25P02 rollback adds a round-trip on error** → Only triggered on
failure, not on the hot path. Acceptable.

## Migration Plan

1. Deploy the code change — no DB migrations, no config changes needed.
2. The sequence desync (if present) can be fixed at any time with:
   ```sql
   SELECT setval('chunks_id_seq', (SELECT MAX(id) FROM chunks));
   ```
3. Rollback: revert the code change; behavior reverts to pre-fix state
   (upsert resumes, 25P02 leak may recur if sequence is still desynced).
