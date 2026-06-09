## MODIFIED Requirements

### Requirement: Staging activates only on cross-pod contention

When the feature is enabled, a replica holding the download lock SHALL begin staging only after it observes a cross-pod waiter for the same NAR hash, recorded as a request marker in the `staging_state` record. A replica using the local (non-distributed) locker SHALL never stage, because no cross-pod waiter can exist. The holder SHALL observe a waiter's request promptly enough to begin staging while the NAR is still in flight.

For an eager-CDC NAR the in-flight window extends beyond the byte download through the entire chunking operation (the per-hash migration lock is held for the whole chunking operation). The holder SHALL keep its staging producer alive for the duration of that chunking window — while it still has the complete in-flight bytes — so that a waiter whose request arrives after the byte download but before chunking completes is still staged and served, rather than missed.

The holder SHALL treat a staging request as a clean no-op only when the NAR is genuinely fully materialized in shared storage — a committed whole-file copy, or a finished chunk set (`total_chunks > 0`) — returning without logging an error. An actively-chunking eager-CDC NAR (`total_chunks == 0` with a live chunker and no whole-file copy in shared storage) is NOT such a case and SHALL still be staged.

#### Scenario: Cross-pod waiter triggers staging

- **WHEN** replica A holds the download lock for hash `H` and is actively downloading
- **AND** replica B fails to acquire the lock and records a staging request for `H`
- **THEN** replica A SHALL observe the request promptly and begin staging the NAR to shared storage while the download is still in flight

#### Scenario: Local locker never stages

- **WHEN** ncps runs with the local (single-instance) locker and the feature is enabled
- **THEN** no staging request can be recorded by another replica
- **AND** the holder SHALL never begin staging

#### Scenario: Request observed after the NAR is fully in shared storage is a clean no-op

- **WHEN** a staging request for hash `H` is observed by the holder only after the NAR is fully materialized in shared storage (a committed whole-file copy, or a finished chunk set with `total_chunks > 0`)
- **THEN** the holder SHALL NOT attempt to stage from an absent temp file
- **AND** it SHALL return without logging an error, because cross-pod waiters serve the completed NAR from shared storage

#### Scenario: Waiter arriving during the eager-CDC chunking window still activates staging

- **WHEN** replica A has finished downloading the bytes of an eager-CDC NAR for hash `H` but is still actively chunking it (its `nar_file` row has `total_chunks == 0` with a live chunker, and no whole-file copy exists in shared storage)
- **AND** replica B records a staging request for `H` during that chunking window
- **THEN** replica A SHALL observe the request and stage the NAR from its live in-flight bytes rather than treating the request as a no-op
- **AND** replica B SHALL be able to serve the complete NAR from staging

### Requirement: Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes carry the compression recorded in `staging_state.compression`; when the client requests the uncompressed (`none`) variant of compressed staged bytes, the reader SHALL decompress on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200.

For the **eager-CDC chunking window** the staged bytes are uncompressed (the temp is decompressed for chunking, so there is no whole-file copy in any compressed form). A request for the **uncompressed** variant SHALL be served the complete NAR from staging. A request for a **compressed** variant cannot be reconstructed from uncompressed staged bytes (ncps has no NAR compressor, and a re-compressed file would not match the narinfo `FileHash`/`FileSize`), so the staging serve SHALL return a not-found and the client SHALL fall back to an upstream that has the original compressed file — never the uncompressed bytes mislabeled as the requested compression.

This relies on the download holder retaining the NAR download lock through the eager-CDC chunking window (see the `cdc-chunking` capability). A cross-pod reader arriving mid-chunking therefore **contends** for the held lock and enters the staging-request path, rather than acquiring a free lock and short-circuiting to chunk-based serving. The reader SHALL NOT re-download or re-chunk the NAR while the holder's chunking is live.

#### Scenario: Non-CDC cross-pod serve during download

- **WHEN** CDC is disabled and replica A is downloading a NAR with replica B waiting
- **AND** staging is active
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200

#### Scenario: Compressed request during active chunking coordinates, then falls back to upstream

- **WHEN** replica A is actively chunking a NAR (its cross-pod-visible `nar_file` row has `total_chunks==0` with a live chunker) and replica B receives a request for a compressed variant (e.g. `.nar.xz`)
- **THEN** replica B SHALL enter download coordination rather than acquiring a free lock and short-circuiting to chunk-based serving
- **AND** because the in-flight bytes are uncompressed, the staging serve SHALL return a not-found so the client falls back to an upstream that has the original compressed file

#### Scenario: Uncompressed request during active chunking still streams from chunks

- **WHEN** replica A is actively chunking a NAR and replica B receives a request for the uncompressed (`none`) variant
- **THEN** replica B SHALL serve progressively from chunks as they are committed, unchanged by the compressed-request coordination behavior

#### Scenario: Reader contends for the held lock mid-chunking and serves an uncompressed request from staging

- **WHEN** the holder retains the download lock while it continues chunking an eager-CDC NAR for hash `H` (no whole-file copy in shared storage)
- **AND** replica B contends for the held lock and receives a request for the uncompressed (`none`) variant of `H`
- **THEN** replica B SHALL enter download coordination, record an in-flight staging request, and serve the complete NAR from staging once parts are available
- **AND** replica B SHALL NOT re-download or re-chunk the NAR

#### Scenario: Eager-CDC compressed cross-pod request falls back to upstream

- **WHEN** CDC is eager and replica A is actively chunking a NAR (its `nar_file` row exists but `total_chunks` is still 0), the staged bytes are uncompressed, and replica B requests a compressed variant
- **THEN** replica B SHALL return a not-found from the staging serve path so the client falls back to an upstream that has the original compressed file
- **AND** replica B SHALL NOT serve the uncompressed staged bytes mislabeled as the requested compression

#### Scenario: Compression transcode on serve (decompression only)

- **WHEN** staged parts hold a compressed form and the client requests the uncompressed (`none`) variant
- **THEN** the reader SHALL decompress the staged bytes on the fly while serving and advertise `none`
- **AND** a request for a compressed variant backed only by uncompressed staged bytes SHALL instead return a not-found (ncps cannot re-compress to a `FileHash`/`FileSize`-matching file), so the client falls back to upstream

#### Scenario: Stalled producer does not yield a truncated success

- **WHEN** a reader is tailing staging parts and the producer stalls before the NAR is complete
- **THEN** the reader SHALL surface a stream error rather than terminating the response with a clean EOF at a truncated length
