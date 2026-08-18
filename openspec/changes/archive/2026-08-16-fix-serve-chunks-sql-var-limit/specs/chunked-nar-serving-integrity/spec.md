## ADDED Requirements

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
