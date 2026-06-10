## MODIFIED Requirements

### Requirement: Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes carry the compression recorded in `staging_state.compression`; when the client requests the uncompressed (`none`) variant of compressed staged bytes, the reader SHALL decompress on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200.

For the **eager-CDC chunking window** the staged bytes are uncompressed (the temp is decompressed for chunking, so there is no whole-file copy in any compressed form). A request for the **uncompressed** variant SHALL be served the complete NAR from staging, preferring staging over progressive chunk reassembly. A request for a **compressed** variant cannot be reconstructed from uncompressed staged bytes, so the staging serve SHALL return a not-found and the client SHALL fall back to an upstream that has the original compressed file.

When in-flight staging is enabled, an **uncompressed** cross-pod read arriving during the eager-CDC chunking window (the holder is actively chunking; the NAR is servable-from-chunks but not finished) SHALL enter download coordination — contending for the holder's retained download lock and recording an in-flight staging request — rather than short-circuiting directly to progressive chunk serving. The coordinating reader SHALL wait, bounded, for the holder's producer to make staging parts available and then serve from staging. Progressive chunk serving from the holder's committed chunks remains the fallback only when in-flight staging is disabled, or when staging parts do not materialize within that bound (e.g. a stalled or errored producer). This relies on the holder retaining the NAR download lock through the chunking window (see the `cdc-chunking` capability).

#### Scenario: Non-CDC cross-pod serve during download

- **WHEN** CDC is disabled and replica A is downloading a NAR with replica B waiting
- **AND** staging is active
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200

#### Scenario: Uncompressed request during active chunking prefers staging

- **WHEN** in-flight staging is enabled, replica A is actively chunking an eager-CDC NAR (its `nar_file` row has `total_chunks == 0` with a live chunker, no whole-file copy in shared storage), and replica B receives a request for the uncompressed (`none`) variant
- **THEN** replica B SHALL enter download coordination, contend for the held download lock, and record an in-flight staging request rather than short-circuiting to progressive chunk serving
- **AND** replica B SHALL serve the complete, byte-identical NAR from staging once parts are available
- **AND** replica B SHALL NOT re-download or re-chunk the NAR

#### Scenario: Progressive chunks remain the fallback when staging cannot serve

- **WHEN** an uncompressed cross-pod read is coordinating during active chunking but in-flight staging is disabled, OR staging parts do not become available within the bounded staging wait (a stalled/errored producer)
- **THEN** replica B SHALL fall back to serving progressively from the holder's committed chunks
- **AND** replica B SHALL surface a stream error rather than a truncated HTTP 200 if reassembly cannot complete

#### Scenario: Compressed request during active chunking coordinates, then falls back to upstream

- **WHEN** replica A is actively chunking a NAR and replica B receives a request for a compressed variant (e.g. `.nar.xz`)
- **THEN** replica B SHALL enter download coordination rather than acquiring a free lock and short-circuiting to chunk-based serving
- **AND** because the in-flight bytes are uncompressed, the staging serve SHALL return a not-found so the client falls back to an upstream that has the original compressed file

#### Scenario: Compression transcode on serve (decompression only)

- **WHEN** staged parts hold a compressed form and the client requests the uncompressed (`none`) variant
- **THEN** the reader SHALL decompress the staged bytes on the fly while serving and advertise `none`
- **AND** a request for a compressed variant backed only by uncompressed staged bytes SHALL instead return a not-found, so the client falls back to upstream

#### Scenario: Stalled producer does not yield a truncated success

- **WHEN** a reader is tailing staging parts and the producer stalls before the NAR is complete
- **THEN** the reader SHALL surface a stream error rather than terminating the response with a clean EOF at a truncated length
