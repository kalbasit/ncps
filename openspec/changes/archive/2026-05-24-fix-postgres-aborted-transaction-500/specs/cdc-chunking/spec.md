## MODIFIED Requirements

### Requirement: CDC chunk insert MUST use ignore-on-conflict, not upsert

When recording a batch of chunks in `recordChunkBatch`, each chunk insert
into the `chunks` table SHALL use `ON CONFLICT (hash) DO NOTHING` (not
`DO UPDATE`). Chunks are content-addressed immutable blobs; an existing
chunk with the same hash is already correct and MUST NOT be touched.

After the INSERT (whether it inserted or skipped), the system SHALL retrieve
the chunk's `id` via a `SELECT WHERE hash = <hash>` query and use that ID
to build the `nar_file_chunks` link. This two-step approach ensures the
correct ID is obtained regardless of whether the row was newly inserted or
pre-existed.

#### Scenario: Chunk is new — INSERT succeeds, ID retrieved from SELECT

- **WHEN** a chunk with a given hash does not exist in the `chunks` table
- **THEN** the INSERT inserts the row and `DO NOTHING` is not triggered
- **AND** the subsequent SELECT retrieves the newly inserted chunk's `id`
- **AND** the `nar_file_chunks` link is created with that `id`

#### Scenario: Chunk already exists — INSERT is skipped, ID retrieved from SELECT

- **WHEN** a chunk with a given hash already exists in the `chunks` table
- **THEN** the INSERT is skipped silently (`DO NOTHING`)
- **AND** the subsequent SELECT retrieves the pre-existing chunk's `id`
- **AND** the `nar_file_chunks` link is created with that `id`

#### Scenario: Duplicate hash in same batch — no conflict error

- **WHEN** a chunk batch contains two entries with the same hash (same content)
- **THEN** the first INSERT inserts the row; the second INSERT is skipped
- **AND** both `nar_file_chunks` links use the same chunk `id`
- **AND** no error is returned

## ADDED Requirements

### Requirement: Transaction failure MUST NOT leave connections in aborted state

After any transaction in `withEntTransactionRetry` fails (whether immediately
or after all retry attempts are exhausted), the system SHALL ensure the
database connection is returned to the pool in a clean, non-aborted state.

If the final transaction error carries PostgreSQL SQLSTATE 25P02
(`in_failed_sql_transaction`), the system SHALL issue an explicit `ROLLBACK`
on the connection before it is returned to the pool. A subsequent query on
the same connection MUST NOT fail with "current transaction is aborted".

#### Scenario: Transaction exhausts retries — connection is clean on return

- **GIVEN** `withEntTransactionRetry` exhausts all retry attempts
- **AND** the final error is a PostgreSQL unique_violation (SQLSTATE 23505)
- **WHEN** the error is returned to the caller
- **THEN** the connection is returned to the pool in a clean state
- **AND** a subsequent non-transactional query on any pooled connection
  succeeds without a 25P02 error

#### Scenario: 25P02 detected — explicit rollback issued

- **GIVEN** a database connection is in PostgreSQL aborted-transaction state
  (SQLSTATE 25P02 — "in_failed_sql_transaction")
- **WHEN** `withEntTransactionRetry` detects this condition on the error
- **THEN** an explicit `ROLLBACK` is issued on that connection
- **AND** the connection is returned to the pool usable for the next query
