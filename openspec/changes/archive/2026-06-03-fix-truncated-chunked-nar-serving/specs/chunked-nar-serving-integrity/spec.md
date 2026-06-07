## ADDED Requirements

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
