## MODIFIED Requirements

### Requirement: In-flight staging contention scenario

The harness SHALL provide a `staging-contention` scenario that proves in-flight NAR staging activates under real multi-replica contention and delivers complete, byte-identical NARs. The scenario MUST launch at least two replicas with a Redis distributed locker and staging enabled, race concurrent clients fetching the same uncached NAR so that lock-losing waiters become staging consumers, and cover both the download window (CDC off) and the chunking window (eager CDC) as independently-scored runs.

For **both** windows the harness MUST race readers while the NAR is still in flight and MUST assert that in-flight staging **activates** on the non-holder replica (the staging-activation log line) — a no-op run (staging never activates) is a FAILURE, not a pass. For the chunking window the harness gates the race on an observed in-flight state (a `nar_files` row with `total_chunks == 0`, or no row yet) so the readers overlap the holder's production; every reader MUST receive a NAR byte-identical to the canonical `nix-store --dump`.

This scenario SHALL remain `local`-mode only: in-flight staging *activation* is a single-shot timing event, and reaching `kubernetes` replicas through `kubectl port-forward` introduces per-request latency jitter that de-synchronizes the race so the lock-holder caches the NAR before cross-pod waiters can contend on an in-flight piece; activation therefore cannot be reliably forced on Kind. The harness MUST report the scenario as SKIPPED (never PASSED) when requested in `kubernetes` mode.

#### Scenario: Concurrent same-NAR fetch activates staging in both windows

- **WHEN** at least two replicas run with `--locker redis` and staging enabled and N clients race to fetch the same large uncached NAR, in either the download window (CDC off) or the chunking window (eager CDC)
- **THEN** at least one lock-losing waiter serves from committed staging parts, evidenced by the staging-activation log line on the non-holder replica

#### Scenario: All racing readers receive identical complete NARs

- **WHEN** the racing clients complete their fetches in either window
- **THEN** every reader receives a NAR whose decompressed content is byte-identical to the canonical store-path NAR and to every other reader, with a truncated or differing body failing even on HTTP 200

#### Scenario: Non-activation is a failure, not a pass

- **WHEN** a run completes without staging ever activating in a window
- **THEN** the harness reports the scenario as FAILED with diagnostics, not as PASSED

#### Scenario: Both protected windows are covered

- **WHEN** the scenario is run
- **THEN** it exercises the download window (CDC off, whole-file NARs) and the chunking window (eager CDC) as separate runs each with its own pass/fail, each asserting staging activation

#### Scenario: Kubernetes mode skips the scenario rather than running it unreliably

- **WHEN** the `staging-contention` scenario is requested with `--mode kubernetes`
- **THEN** the harness reports it as SKIPPED (topology/timing unsupported in that mode), never PASSED, because port-forward jitter makes single-shot in-flight activation unreliable on Kind
