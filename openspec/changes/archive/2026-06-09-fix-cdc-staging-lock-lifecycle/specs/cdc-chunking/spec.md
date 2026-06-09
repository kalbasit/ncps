## ADDED Requirements

### Requirement: The NAR download lock MUST be held through the eager-CDC chunking window

For an eager-CDC NAR (chunked progressively during download, with no whole-file copy committed to shared storage), the replica performing the download SHALL retain the per-hash NAR download lock until chunking completes, rather than releasing it when the raw bytes are first stored (the start of chunking). Holding the lock through the chunking window SHALL cause a cross-pod reader arriving mid-chunking to contend for the lock — entering download coordination and recording an in-flight staging request — instead of acquiring a free lock and short-circuiting to chunk-based serving.

The eager-CDC in-flight bytes are decompressed (the temp is decompressed for chunking), so a request for the **uncompressed** (`none`) variant SHALL be served the complete NAR from in-flight staging, while a request for a **compressed** variant (e.g. `.nar.xz`) — which cannot be reconstructed from decompressed bytes (ncps has no NAR compressor, and a re-compressed file would not match the narinfo `FileHash`/`FileSize`) — SHALL return not-found so the client falls back to an upstream that has the original compressed file.

This SHALL NOT change download-lock release timing for non-CDC or lazy-CDC downloads, which have no progressive chunking window. Holder death during the chunking window SHALL remain recoverable through download-lock TTL expiry and takeover, unchanged.

#### Scenario: Download lock is held until chunking completes (eager CDC)

- **WHEN** a replica downloads an eager-CDC NAR for hash `H` and begins chunking it (its `nar_file` row is created with `total_chunks == 0`)
- **THEN** the replica SHALL continue to hold the per-hash NAR download lock until chunking completes
- **AND** a cross-pod reader attempting to coordinate `H` during that window SHALL contend for the lock rather than acquire it

#### Scenario: Cross-pod uncompressed request mid-chunking serves the complete NAR from staging

- **WHEN** replica A holds the download lock and is actively chunking an eager-CDC NAR for hash `H`
- **AND** replica B receives a request for the uncompressed (`none`) variant of `H`
- **THEN** replica B SHALL contend for the lock, record an in-flight staging request, and serve the complete NAR from staging once parts are available, rather than fragile progressive chunk reassembly

#### Scenario: Cross-pod compressed request mid-chunking falls back to upstream, not corrupt or 404-from-chunks

- **WHEN** replica A is actively chunking an eager-CDC NAR for hash `H` and replica B receives a request for a compressed variant (e.g. `.nar.xz`) of `H`
- **THEN** replica B SHALL return a not-found from the staging serve path so the client falls back to an upstream that has the original compressed file
- **AND** replica B SHALL NOT serve the decompressed in-flight bytes mislabeled as the requested compression

#### Scenario: Non-CDC and lazy-CDC download-lock timing unchanged

- **WHEN** a NAR is downloaded with CDC disabled, or with lazy chunking enabled
- **THEN** the download-lock release timing SHALL be unchanged from prior behavior, because there is no progressive chunking window in which the whole NAR is absent from shared storage

#### Scenario: Holder death during chunking is recoverable

- **WHEN** the replica holding the download lock dies while chunking an eager-CDC NAR for hash `H`
- **THEN** the download lock SHALL expire via its TTL
- **AND** another replica SHALL be able to take over and re-download the NAR, unchanged from prior takeover behavior

#### Scenario: Steady-state compressed-only-as-chunks 404 is unchanged

- **WHEN** a NAR is finished chunking (`total_chunks > 0`), its whole-file copy is gone, and a client requests a compressed variant
- **THEN** the serve path SHALL still return HTTP 404 so the client falls back to an upstream cache, unchanged by the held-lock behavior
