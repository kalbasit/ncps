# inflight-staging-contention-e2e

## Purpose

End-to-end validation that in-flight NAR staging actually engages under real multi-replica contention: an e2e driver races concurrent readers against an in-flight download across a distributed-locker (Redis) cluster and proves every reader receives a complete, byte-identical NAR in both the download and chunking windows.

## Requirements

### Requirement: Contention-activated in-flight staging driver

The driver MUST spawn at least two `ncps` replicas via `dev-scripts/run.py` configured with `--locker redis` and `--inflight-staging`, then issue concurrent fetches of the same store path so that the in-flight NAR staging feature is *activated* (a second reader becomes a waiter on an in-flight download), and it MUST fail the run if staging never activated.

#### Scenario: Multi-replica redis-locker cluster is launched with staging enabled
- **WHEN** the driver starts the cluster
- **THEN** at least two `ncps` instances are running, each reporting an effective configuration of `locker: redis` and `inflight_staging: true` (read from `state.json`)
- **AND** the driver aborts with a clear error if any instance reports the local locker or `inflight_staging: false`

#### Scenario: Concurrent same-NAR fetch activates staging
- **WHEN** the cache is empty and N concurrent clients request the same (large) store path's `.narinfo` and NAR simultaneously
- **THEN** exactly one fetch triggers the upstream download and the remaining clients become waiters that activate in-flight staging
- **AND** the driver verifies activation via an exported staging signal (state/metric), failing the run if no waiter ever activated staging (a no-op run MUST NOT be reported as a pass)

### Requirement: Complete byte-identical NAR delivery under contention

Every concurrent reader MUST receive a complete NAR whose decompressed content is byte-identical to the canonical store-path contents and to every other reader's received NAR; a truncated, short, or differing body MUST fail the run even when the HTTP status is 200.

#### Scenario: All racing readers receive identical complete NARs
- **WHEN** all N concurrent fetches complete
- **THEN** each reader's received NAR decompresses to the same content digest
- **AND** that digest matches the canonical store-path NAR
- **AND** any reader whose body is short/truncated/mismatched fails the run despite a 200 response

### Requirement: Coverage of both protected windows

The driver MUST exercise contention in both the download window (CDC disabled, whole-file NARs) and the chunking window (CDC enabled), since in-flight staging protects readers in both, and it MUST report per-window pass/fail so a regression can be localized to one window.

#### Scenario: Download-window contention (CDC off)
- **WHEN** the driver runs with CDC disabled and drives concurrent same-NAR fetches
- **THEN** all readers receive complete byte-identical NARs and the download-window phase is reported pass/fail independently

#### Scenario: Chunking-window contention (CDC on)
- **WHEN** the driver runs with `--enable-cdc` and drives concurrent same-NAR fetches
- **THEN** all readers receive complete byte-identical NARs and the chunking-window phase is reported pass/fail independently

### Requirement: Storage-backend matrix

The driver MUST support running the contention scenario against both the `local` (shared filesystem path) and `s3` storage backends, selectable per run, because the distributed-locker staging path differs between a shared local path and object storage.

#### Scenario: Backend is selectable
- **WHEN** the driver is invoked with a storage selection of `local` or `s3`
- **THEN** the spawned cluster uses that backend for all replicas
- **AND** the same contention and byte-identity assertions are applied regardless of backend

### Requirement: One-command fixed-port wrapper

A wrapper script MUST start the fixed-port dev dependency stack (`nix run .#deps`, which provides Redis), run the contention driver with pass-through arguments, and tear the stack down on exit, mirroring `dev-scripts/test-cdc-lifecycle-auto.sh`.

#### Scenario: Wrapper manages the dependency lifecycle
- **WHEN** a user runs the wrapper with driver arguments
- **THEN** the fixed-port backing services (including Redis) are started and waited for, the driver runs, and the services are torn down on exit even if the driver fails
