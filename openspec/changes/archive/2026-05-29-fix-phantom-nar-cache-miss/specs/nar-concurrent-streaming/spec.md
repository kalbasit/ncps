## ADDED Requirements

### Requirement: Progressive chunk streaming MUST NOT deliver a truncated NAR body

When serving a NAR via progressive CDC streaming (`streamProgressiveChunks`/`getNarFromChunks`),
the system SHALL NOT emit a successful (HTTP 200) response whose body is shorter than the NAR's
declared size. If chunks stop arriving before the full NAR is produced — because chunking was
aborted, stalled past the lock TTL, or the producing download failed — the streaming path SHALL
surface an error (so the client sees a failed transfer / retryable condition) rather than closing
a short, well-formed-looking body.

When the response has not yet committed bytes to the client, the path SHALL prefer falling back to
a synchronous upstream re-download over emitting a partial body.

#### Scenario: Aborted chunking does not yield a short 200

- **GIVEN** a NAR for hash `H` is being served via progressive streaming
- **AND** chunking for `H` is aborted (`total_chunks` stays 0, `chunking_started_at` becomes NULL)
  before all bytes are produced
- **WHEN** the streaming goroutine observes no further chunks
- **THEN** it SHALL surface an error on the stream
- **AND** SHALL NOT terminate the response as a successful full transfer

#### Scenario: No chunks available falls back to re-download before sending bytes

- **GIVEN** `getNarFromChunks` is entered for hash `H`
- **AND** `total_chunks = 0` and `chunking_started_at` is NULL (no chunks will arrive)
- **AND** no bytes have yet been written to the client
- **WHEN** the system resolves how to serve `H`
- **THEN** it SHALL attempt a synchronous upstream re-download of `H`
- **AND** SHALL NOT return a terminal `storage.ErrNotFound` without that attempt

#### Scenario: Stalled chunk producer is treated as failure, not completion

- **GIVEN** progressive streaming for hash `H` is waiting on the chunk producer
- **AND** the producer has made no progress for longer than `cdcChunkingLockTTL`
- **AND** the NAR is not otherwise present (no whole-file, `total_chunks = 0`)
- **WHEN** the wait elapses
- **THEN** the streaming path SHALL surface an error rather than completing the response
