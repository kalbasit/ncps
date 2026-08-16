# Capability Spec: Chunked NAR Serving Integrity

## Purpose

Guarantees that NARs stored as content-defined chunks are never served as
truncated HTTP responses. When a completed chunked NAR cannot be fully
reassembled (e.g. junction links were lost via a cascade delete), the cache
MUST resolve the request to a clean `404` before committing any response body,
rather than returning an `HTTP 200` with a body that fails mid-stream. The
completeness check applies only to the completed-chunk fast path and must not
disturb legitimate in-progress (progressive) chunking.

## Requirements

### Requirement: A completed chunked NAR that cannot be reassembled MUST NOT be served as a truncated HTTP 200

The system SHALL NOT return an `HTTP 200` response whose body it cannot fully
produce. When serving a chunked NAR on the completed-chunk fast path
(`total_chunks > 0`), the cache MUST verify — **before** the HTTP layer commits a
status line or `Content-Length` — that the NAR is reassemblable: the number of
`nar_file_chunks` junction links MUST equal `total_chunks`. If the NAR is not
reassemblable, `GetNar`/`getNarFromChunks` SHALL return `storage.ErrNotFound`
synchronously rather than returning a reader that fails mid-stream. The completeness
failure that today surfaces as `expected N chunks but got M` inside the streaming
goroutine MUST instead be detected up front.

#### Scenario: Completed chunked NAR with a missing junction link resolves to 404

- **GIVEN** a `nar_file` record for hash `H` with `total_chunks = N` and `N > 0`
- **AND** fewer than `N` `nar_file_chunks` links exist for `H` (links were lost, e.g. via the `chunks` cascade delete)
- **WHEN** a client requests `GET /nar/{H}.nar`
- **THEN** `getNarFromChunks` SHALL return `storage.ErrNotFound` before any response body is written
- **AND** the HTTP handler SHALL respond `HTTP 404 Not Found`
- **AND** the client SHALL NOT receive an `HTTP 200` with a truncated body

#### Scenario: Completeness is validated before the response is committed

- **GIVEN** a completed chunked NAR (`total_chunks > 0`) that is missing one or more chunk links
- **WHEN** the cache prepares to serve it
- **THEN** the completeness check SHALL run before `io.Pipe`/`WriteHeader`/`Content-Length`
- **AND** no partial bytes SHALL be written to the client before the error is detected

#### Scenario: A fully-linked completed chunked NAR is still served normally

- **GIVEN** a `nar_file` record for hash `H` with `total_chunks = N` and exactly `N` junction links
- **WHEN** a client requests `GET /nar/{H}.nar`
- **THEN** the cache SHALL stream the reassembled NAR with `HTTP 200`
- **AND** the completeness check SHALL NOT alter the existing successful serve path

### Requirement: The synchronous completeness check MUST NOT be applied to in-progress (progressive) chunking

The completeness validation SHALL apply only to the completed-chunk fast path
(`total_chunks > 0`). The progressive path (`total_chunks = 0`,
`chunking_started_at` set), which legitimately streams chunks as they appear and
waits for the next chunk, MUST remain unchanged so that a NAR being chunked
concurrently (including by another instance in an HA deployment) is not falsely
resolved to `404`. `total_chunks` is the completion latch: it is set only after all
junction links are durably committed, so `total_chunks > 0 && links < total_chunks`
is always genuine post-completion loss, never a mid-chunking race.

#### Scenario: Mid-chunking NAR is not 404'd by the completeness check

- **GIVEN** a `nar_file` record for hash `H` with `total_chunks = 0` and `chunking_started_at` set (chunking in progress)
- **WHEN** a client requests `GET /nar/{H}.nar`
- **THEN** the cache SHALL take the progressive streaming path
- **AND** the completed-path completeness check SHALL NOT run for `H`
- **AND** the request SHALL NOT be resolved to `404` on account of incomplete links

### Requirement: The completed-chunk fast serve path MUST scale beyond database driver parameter limits

MUST reassemble and serve a completed chunked NAR regardless of how many chunks it
has, even when that count exceeds the database driver's bound-parameter limit
(SQLite 32766, PostgreSQL 65535). When a NAR has completed chunking
(`total_chunks > 0`), the fast serve path (`streamCompleteChunks`) SHALL retrieve
its `nar_file_chunks` links in bounded batches ordered by `chunk_index`, rather
than in a single query whose bound-parameter count grows with the chunk count. The
batch size SHALL be a fixed internal constant well under every supported driver's
limit. The ordered sequence of chunk hashes produced by the batched retrieval MUST
be identical to the sequence a single ordered query would produce, so the streamed
NAR bytes are unchanged.

Before this change, `streamCompleteChunks` issued
`NarFileChunk.Query()…WithChunk().All(ctx)`, whose eager-load compiled to
`SELECT … FROM chunks WHERE id IN ($1 … $N)` with one parameter per chunk. For a
NAR with more chunks than the driver limit (e.g. an ~8 GB NAR at the default 64 KiB
average chunk size yields ~124k chunks), the query failed with `too many SQL
variables`, and the client received an `HTTP 200` with a truncated body
(`bad archive: unexpected end of nar`).

#### Scenario: NAR with chunk count above the SQLite parameter limit serves successfully

- **WHEN** a completed chunked NAR `H` has `total_chunks` greater than the SQLite bound-parameter limit (> 32766) and a client requests `GET /nar/<H>.nar` against a SQLite-backed cache
- **THEN** the fast serve path SHALL retrieve all chunk links in bounded batches without raising `too many SQL variables`
- **AND** the cache SHALL stream the fully reassembled NAR with `HTTP 200`
- **AND** the response body SHALL contain every chunk exactly once, in ascending `chunk_index` order

#### Scenario: Batched retrieval preserves chunk order and completeness

- **WHEN** the fast serve path retrieves the chunk links for a completed NAR whose chunk count spans multiple batches
- **THEN** the concatenated per-batch results SHALL equal the chunk-hash sequence of a single query ordered by `chunk_index`
- **AND** the completeness check (collected chunk count equals `total_chunks`) SHALL still run and SHALL still return `storage.ErrNotFound` when links are missing

#### Scenario: NARs below the parameter limit are unaffected

- **WHEN** a completed chunked NAR has a chunk count at or below the driver parameter limit
- **THEN** the batched retrieval SHALL produce the same ordered chunk-hash sequence and the same streamed bytes as before this change
- **AND** the existing successful serve path behavior SHALL NOT change
