## MODIFIED Requirements

### Requirement: In-flight staging contention scenario

The harness SHALL provide a `staging-contention` scenario that proves correct cross-pod NAR serving under real multi-replica contention and delivers complete, byte-identical NARs. The scenario MUST launch at least two replicas with a Redis distributed locker and staging enabled, race concurrent clients fetching the same uncached NAR, and cover both the download window (CDC off) and the chunking window (eager CDC) as independently-scored runs.

For the **download window (CDC off)** there are no chunks to stream, so a lock-losing waiter MUST become an in-flight staging consumer. This window MUST FAIL if in-flight staging never activates (a no-op run is a failure).

For the **chunking window (eager CDC)** the cross-pod reader of the uncompressed `.nar` is served *progressively from the holder's committed chunks* — the designed behavior (#1289) — rather than from in-flight staging, which is a download-window mechanism. The harness MUST race readers while the eager-CDC download+chunk is still in flight (gating the race on an observed in-flight state — a `nar_files` row with `total_chunks == 0` or no row yet — so the readers overlap the holder's production rather than serving an already-complete chunk set). It MUST then assert that every reader received a complete NAR byte-identical to the canonical `nix-store --dump`, AND that the NAR was downloaded and chunked by **exactly one replica** — i.e. the contending cross-pod readers served from the shared chunk set with no re-download and no re-chunk. The harness MUST NOT assert in-flight staging activation for the chunking window, because eager-CDC uncompressed cross-pod reads are correctly served from chunks, not staging.

This scenario SHALL remain `local`-mode only: in-flight staging *activation* (the download window) is a single-shot timing event, and reaching `kubernetes` replicas through `kubectl port-forward` introduces per-request latency jitter that de-synchronizes the race so the lock-holder caches the NAR before cross-pod waiters can contend on an in-flight piece; activation therefore cannot be reliably forced on Kind. The harness MUST report the scenario as SKIPPED (never PASSED) when requested in `kubernetes` mode.

#### Scenario: Download-window concurrent fetch activates staging

- **WHEN** at least two replicas run with `--locker redis` and staging enabled, CDC is off, and N clients race to fetch the same large uncached NAR
- **THEN** at least one lock-losing waiter serves from committed staging parts, evidenced by the staging-activation log line
- **AND** the window FAILS if staging never activates (a no-op run is a failure, not a pass)

#### Scenario: All racing readers receive identical complete NARs

- **WHEN** the racing clients complete their fetches in either window
- **THEN** every reader receives a NAR whose decompressed content is byte-identical to the canonical store-path NAR and to every other reader, with a truncated or differing body failing even on HTTP 200

#### Scenario: Both protected windows are covered

- **WHEN** the scenario is run
- **THEN** it exercises the download window (CDC off, whole-file NARs) and the chunking window (eager CDC) as separate runs each with its own pass/fail

#### Scenario: Chunking-window readers race an in-flight eager-CDC download

- **WHEN** the chunking window runs and the narinfo prime has triggered the background eager-CDC download+chunk
- **THEN** the harness gates the reader race on an observed in-flight state (a `nar_files` row with `total_chunks == 0`, or no row yet because the download is still in progress) and fires the readers while the holder is producing the NAR
- **AND** if the NAR is already fully materialized before the race, the harness still runs the race against the committed chunk set and the correctness assertions below still apply

#### Scenario: Chunking-window cross-pod readers serve from chunks with no re-download or re-chunk

- **WHEN** the chunking-window readers race the uncompressed `.nar` across two replicas while one replica holds and produces the eager-CDC NAR
- **THEN** every reader receives a complete NAR byte-identical to the canonical `nix-store --dump`
- **AND** the NAR is downloaded and chunked by exactly one replica (the per-hash chunking lock appears on a single replica), proving the cross-pod readers served from the shared chunk set rather than re-downloading or re-chunking
- **AND** the harness does NOT require in-flight staging to activate in this window

#### Scenario: Kubernetes mode skips the scenario rather than running it unreliably

- **WHEN** the `staging-contention` scenario is requested with `--mode kubernetes`
- **THEN** the harness reports it as SKIPPED (topology/timing unsupported in that mode), never PASSED, because port-forward jitter makes single-shot in-flight activation unreliable on Kind
