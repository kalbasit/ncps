## Why

CDC chunk recording fails with "duplicate key value violates unique constraint
`chunks_pkey`" after 5 retries, leaving the PostgreSQL connection in an aborted-
transaction state (SQLSTATE 25P02). A subsequent narinfo query on that connection
returns "current transaction is aborted", causing ncps to return HTTP 500.

## What Changes

- Change the `chunks` table insert from `ON CONFLICT (hash) DO UPDATE SET
  updated_at = ...` to `ON CONFLICT (hash) DO NOTHING`. The upsert pattern
  fails when a single bulk insert contains two rows with the same chunk hash
  (PostgreSQL: "ON CONFLICT DO UPDATE command cannot affect row a second time").
  Since chunks are content-addressed, the existing row is correct; skipping the
  duplicate is the right behavior — same as `nar_file_chunks` already does.
- Ensure that after any transaction failure in the CDC chunk recording path, the
  aborted connection is not returned to the pool with live transaction state. Add
  explicit rollback verification and/or a connection health check before reuse so
  that a narinfo query on a pool connection never inherits SQLSTATE 25P02.

## Capabilities

### New Capabilities
_None._

### Modified Capabilities
- `cdc-chunking`: chunk insert conflict strategy changes from upsert to ignore;
  adds a requirement that transaction failures MUST NOT leave pool connections in
  an aborted state visible to subsequent non-transactional queries.

## Impact

- `pkg/cache/cache.go` — `recordChunkBatch`: change `OnConflictColumns(entchunk.FieldHash).Update(...)` to `.Ignore()`
- `pkg/database/client.go` — `WithTransaction` or its callers: ensure rollback cleans connection state before returning to pool
- No API surface changes; no schema migrations needed
- Performance: negligible — removes redundant `updated_at` writes on duplicate chunks
- No impact on SQLite or MySQL (both handle the conflict correctly already)
