## ADDED Requirements

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

When the feature is enabled, a replica holding the download lock SHALL begin staging only after it observes a cross-pod waiter for the same NAR hash, recorded as a request marker in the `staging_state` record. A replica using the local (non-distributed) locker SHALL never stage, because no cross-pod waiter can exist.

#### Scenario: Cross-pod waiter triggers staging

- **WHEN** replica A holds the download lock for hash `H` and is actively downloading
- **AND** replica B fails to acquire the lock and records a staging request for `H`
- **THEN** replica A SHALL observe the request within a bounded interval and begin staging the NAR to shared storage

#### Scenario: Local locker never stages

- **WHEN** ncps runs with the local (single-instance) locker and the feature is enabled
- **THEN** no staging request can be recorded by another replica
- **AND** the holder SHALL never begin staging

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

A waiting replica that observes available staging parts SHALL tail them to reassemble and serve a complete, byte-correct NAR, regardless of whether CDC is disabled, lazy, or eager. The served bytes SHALL carry the compression recorded in `staging_state.compression`; when the client requests a different compression, the reader SHALL transcode on the fly at parity with the same-pod streaming path. A reader SHALL NOT deliver a truncated NAR as an HTTP 200.

#### Scenario: Non-CDC cross-pod serve during download

- **WHEN** CDC is disabled and replica A is downloading a NAR with replica B waiting
- **AND** staging is active
- **THEN** replica B SHALL serve the complete NAR from staging parts with HTTP 200

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
