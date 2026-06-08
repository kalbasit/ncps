## MODIFIED Requirements

### Requirement: Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes SHALL carry the compression recorded in `staging_state.compression`; when the client requests a different compression, the reader SHALL transcode on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200. When the holder is alive and staging parts are available, a waiting replica SHALL prefer serving from staging over routing to chunk-based serving, so that an actively-chunking (eager-CDC) NAR is never returned to the client as an HTTP 404 merely because the requested compression cannot be produced from chunks. A replica that observes an actively-chunking NAR (visible cross-pod, not yet finished) and receives a request for a compression that cannot be produced from chunks SHALL enter download coordination (recording a staging request and serving from staging) rather than short-circuiting to chunk-based serving and returning HTTP 404.

#### Scenario: Compressed request during active chunking coordinates instead of short-circuiting to chunks

- **WHEN** replica A is actively chunking a NAR (its cross-pod-visible `nar_file` row has `total_chunks==0` with a live chunker) and replica B receives a request for a compressed variant (e.g. `.nar.xz`)
- **THEN** replica B SHALL NOT short-circuit to chunk-based serving (which cannot produce the compressed variant)
- **AND** replica B SHALL enter download coordination and record an in-flight staging request, so the request can be served from staging once parts are available (the staging serve itself being completed by the producer)

#### Scenario: Uncompressed request during active chunking still streams from chunks

- **WHEN** replica A is actively chunking a NAR and replica B receives a request for the uncompressed (`none`) variant
- **THEN** replica B SHALL serve progressively from chunks as they are committed, unchanged by the compressed-request coordination behavior

#### Scenario: Non-CDC cross-pod serve during download

- **WHEN** CDC is disabled and replica A is downloading a NAR with replica B waiting
- **AND** staging is active
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200
