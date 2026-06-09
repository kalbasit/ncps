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

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes SHALL carry the compression recorded in `staging_state.compression`; when the client requests a different compression, the reader SHALL transcode on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200. When the holder is alive and staging parts are available, a waiting replica SHALL prefer serving from staging over routing to chunk-based serving, so that an actively-chunking (eager-CDC) NAR is never returned to the client as an HTTP 404 merely because the requested compression cannot be produced from chunks. A replica that observes an actively-chunking NAR (visible cross-pod, not yet finished) and receives a request for a compression that cannot be produced from chunks SHALL enter download coordination (recording an in-flight staging request) rather than short-circuiting to chunk-based serving and returning HTTP 404.

This coordination relies on the download holder retaining the NAR download lock through the eager-CDC chunking window (see the `cdc-chunking` capability). A cross-pod reader arriving mid-chunking therefore **contends** for the held lock and enters the staging-request path (recording an in-flight staging request and serving from staging once parts are available), rather than acquiring a free lock and short-circuiting to chunk-based serving — which would return HTTP 404 for a compressed request. The reader SHALL NOT re-download or re-chunk the NAR while the holder's chunking is live.

#### Scenario: Non-CDC cross-pod serve during download

- **WHEN** CDC is disabled and replica A is downloading a NAR with replica B waiting
- **AND** staging is active
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200

#### Scenario: Compressed request during active chunking coordinates instead of short-circuiting to chunks

- **WHEN** replica A is actively chunking a NAR (its cross-pod-visible `nar_file` row has `total_chunks==0` with a live chunker) and replica B receives a request for a compressed variant (e.g. `.nar.xz`)
- **THEN** replica B SHALL NOT short-circuit to chunk-based serving (which cannot produce the compressed variant)
- **AND** replica B SHALL enter download coordination and record an in-flight staging request, so the request can be served from staging once parts are available

#### Scenario: Uncompressed request during active chunking still streams from chunks

- **WHEN** replica A is actively chunking a NAR and replica B receives a request for the uncompressed (`none`) variant
- **THEN** replica B SHALL serve progressively from chunks as they are committed, unchanged by the compressed-request coordination behavior

#### Scenario: Reader contends for the held lock mid-chunking and serves from staging, not a 404

- **WHEN** the holder retains the download lock while it continues chunking an eager-CDC NAR for hash `H` (no whole-file copy in shared storage)
- **AND** replica B contends for the held lock and receives a request for a compressed variant of `H`
- **THEN** replica B SHALL enter download coordination, record an in-flight staging request, and serve from staging once parts are available
- **AND** replica B SHALL NOT return HTTP 404 by short-circuiting to chunk-based serving, and SHALL NOT re-download or re-chunk the NAR

#### Scenario: Eager-CDC cross-pod serve prefers staging over chunk routing

- **WHEN** CDC is eager and replica A holds the download lock and is actively chunking a NAR (its `nar_file` row exists but `total_chunks` is still 0) with replica B waiting
- **AND** staging is active and the holder has produced staging parts
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200, transcoding to the requested compression
- **AND** replica B SHALL NOT return HTTP 404 by routing to chunk-based serving that cannot satisfy the requested compression

#### Scenario: Compression transcode on serve

- **WHEN** staged parts hold compression `C` and the client requests a different compression
- **THEN** the reader SHALL transcode the staged bytes to the requested compression while serving
- **AND** the response SHALL advertise the compression actually served

#### Scenario: Stalled producer does not yield a truncated success

- **WHEN** a reader is tailing staging parts and the producer stalls before the NAR is complete
- **THEN** the reader SHALL surface a stream error rather than terminating the response with a clean EOF at a truncated length
