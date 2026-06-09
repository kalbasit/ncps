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

### Requirement: Predictive narinfo none steers eager-CDC cross-pod readers to the uncompressed staging serve

When eager CDC is active, ncps advertises the narinfo as `Compression: none` (see the `cdc-chunking` capability), so a cross-pod reader requests the uncompressed `.nar` variant during the chunking window. The uncompressed request SHALL be satisfied from staging (or progressive chunks) per the existing cross-pod staging requirement. The compressed-variant upstream fallback SHALL remain ONLY as a defensive backstop for a directly-constructed `.nar.xz` request (e.g., a client holding a stale `xz` narinfo). Under eager CDC the common cross-pod path SHALL NOT exercise the upstream compressed fallback, because no client requests `.nar.xz`.

#### Scenario: Eager-CDC cross-pod reader fetches narinfo then serves .nar from staging

- **WHEN** eager CDC is active, replica A is actively chunking a NAR, and replica B fetches the narinfo for that hash
- **THEN** the narinfo advertises `Compression: none` / `.nar`
- **AND** replica B requests the uncompressed `.nar` and serves it from staging with HTTP 200
- **AND** replica B does NOT request `.nar.xz` and does NOT fall back to upstream

#### Scenario: Stale xz narinfo still falls back defensively

- **WHEN** a client holds a stale narinfo advertising `Compression: xz` for an eager-CDC NAR that exists only as uncompressed in-flight bytes
- **AND** it requests `.nar.xz` cross-pod during the chunking window
- **THEN** the staging serve returns not-found and the client falls back to an upstream that has the original compressed file
- **AND** the uncompressed staged bytes are NOT served mislabeled as `xz`

