## MODIFIED Requirements

### Requirement: Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes SHALL carry the compression recorded in `staging_state.compression`; when the client requests a different compression, the reader SHALL transcode on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200. When the holder is alive and staging parts are available, a waiting replica SHALL prefer serving from staging over routing to chunk-based serving, so that an actively-chunking (eager-CDC) NAR is never returned to the client as an HTTP 404 merely because the requested compression cannot be produced from chunks.

#### Scenario: Non-CDC cross-pod serve during download

- **WHEN** CDC is disabled and replica A is downloading a NAR with replica B waiting
- **AND** staging is active
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200

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
