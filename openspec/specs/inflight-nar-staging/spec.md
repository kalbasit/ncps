# inflight-nar-staging

## Purpose

Serve a complete, byte-correct NAR to other replicas **while it is still being downloaded** by the holding replica, by staging the in-flight NAR to shared storage as ordered, immutable, fixed-size part-objects once a cross-pod waiter is detected. This closes the cross-pod serve-during-download gap (#660) for all CDC modes and subsumes the eager-CDC chunking-window gap (#1289). The feature is flag-gated and off by default, with zero overhead until cross-pod contention.

## Requirements

### Requirement: In-flight NAR staging is gated by an explicit flag, disabled by default

The in-flight NAR staging feature SHALL be controlled by `--cache-inflight-staging-enabled` (env `CACHE_INFLIGHT_STAGING_ENABLED`), which SHALL default to `false`. When disabled, the download and serve paths SHALL behave exactly as before this change, with no staging records written and no staging part-objects created.

#### Scenario: Feature disabled by default

- **WHEN** ncps starts without `--cache-inflight-staging-enabled` set
- **THEN** the feature SHALL be inactive
- **AND** no `staging_state` records SHALL be created and no staging part-objects SHALL be written during any download

#### Scenario: Feature enabled but no contention

- **WHEN** `--cache-inflight-staging-enabled=true` and a single reader requests a NAR that is being downloaded with no other replica waiting
- **THEN** the download SHALL proceed to temp and final storage normally
- **AND** no staging part-objects SHALL be written (zero staging overhead until contention)

### Requirement: Staging activates only on cross-pod contention

When the feature is enabled, a replica holding the download lock SHALL begin staging only after it observes a cross-pod waiter for the same NAR hash, recorded as a request marker in the `staging_state` record. A replica using the local (non-distributed) locker SHALL never stage, because no cross-pod waiter can exist. The holder SHALL observe a waiter's request promptly enough to begin staging while the download is still in flight, and if the download has already completed when the request is observed the holder SHALL treat staging as a clean no-op (the NAR is already in shared storage) rather than reporting an error.

#### Scenario: Cross-pod waiter triggers staging

- **WHEN** replica A holds the download lock for hash `H` and is actively downloading
- **AND** replica B fails to acquire the lock and records a staging request for `H`
- **THEN** replica A SHALL observe the request promptly and begin staging the NAR to shared storage while the download is still in flight

#### Scenario: Local locker never stages

- **WHEN** ncps runs with the local (single-instance) locker and the feature is enabled
- **THEN** no staging request can be recorded by another replica
- **AND** the holder SHALL never begin staging

#### Scenario: Request observed after the download completed is a clean no-op

- **WHEN** a staging request for hash `H` is observed by the holder only at or after the download has completed (the in-flight temp file has already been committed to shared storage)
- **THEN** the holder SHALL NOT attempt to stage from the absent temp file
- **AND** it SHALL return without logging an error, because cross-pod waiters serve the completed NAR from shared storage

### Requirement: The in-flight NAR is staged as ordered immutable part-objects, backfilled from the start

When staging activates, the holder SHALL write the NAR to shared storage as ordered, immutable, fixed-size part-objects. Upon activation it SHALL first backfill all already-downloaded bytes as parts starting from offset zero, then append further parts as bytes arrive, advancing `staging_state.parts_available` as each part becomes durably readable. The part size SHALL be configurable via `--cache-inflight-staging-part-size` and SHALL default to 8 MiB.

#### Scenario: Late waiter still receives a complete NAR

- **WHEN** replica B records a staging request after replica A has already downloaded part of the NAR
- **THEN** replica A SHALL backfill the already-downloaded prefix as parts beginning at offset zero
- **AND** replica B SHALL be able to reassemble the complete NAR from part zero onward

#### Scenario: Parts become readable progressively

- **WHEN** the holder commits each staging part-object
- **THEN** `staging_state.parts_available` SHALL advance only after the corresponding part-object is durably readable from shared storage

### Requirement: Cross-pod readers serve a complete, byte-correct NAR from staging in all CDC modes

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes SHALL carry the compression recorded in `staging_state.compression`; when the client requests a different compression, the reader SHALL transcode on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200. When the holder is alive and staging parts are available, a waiting replica SHALL prefer serving from staging over routing to chunk-based serving, so that an actively-chunking (eager-CDC) NAR is never returned to the client as an HTTP 404 merely because the requested compression cannot be produced from chunks. A replica that observes an actively-chunking NAR (visible cross-pod, not yet finished) and receives a request for a compression that cannot be produced from chunks SHALL enter download coordination (recording an in-flight staging request) rather than short-circuiting to chunk-based serving and returning HTTP 404.

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

### Requirement: Staging part-objects are reclaimed after completion plus a configurable grace period

Staging part-objects and their `staging_state` record SHALL be garbage-collected once the NAR's final representation (whole file or chunks, per mode) is committed, plus a configurable retention grace period set by `--cache-inflight-staging-retention`, draining in-flight readers before deletion. A periodic sweep SHALL reclaim staging records whose holder died and were never taken over, detected by `staging_state.updated_at` staleness (falling back to `staging_state.created_at` when `updated_at` is absent) together with `status`.

#### Scenario: Reclaim after grace

- **WHEN** a NAR's final representation is committed and the retention grace period elapses
- **THEN** the staging part-objects SHALL be deleted
- **AND** subsequent reads SHALL serve from the final representation, not from staging

#### Scenario: Orphaned staging swept after holder death

- **WHEN** the holder dies mid-staging and no replica takes over for longer than the sweep threshold
- **THEN** the periodic sweep SHALL reclaim the orphaned staging part-objects and record

### Requirement: Holder death during staging is recovered by restarting the download from zero

If the staging holder dies, its download lock SHALL expire and a waiting replica SHALL take over by re-acquiring the lock and restarting the download from upstream from offset zero. The dead holder's partial staging part-objects SHALL be discarded and `staging_state` reset; the new holder SHALL re-stage from zero if contention persists. A reader SHALL never be served a truncated NAR as a result of holder death.

#### Scenario: Takeover restarts and re-stages

- **WHEN** replica A dies while staging hash `H` and replica B re-acquires the expired lock
- **THEN** replica B SHALL restart the download from upstream from offset zero
- **AND** replica A's partial staging part-objects SHALL be discarded
