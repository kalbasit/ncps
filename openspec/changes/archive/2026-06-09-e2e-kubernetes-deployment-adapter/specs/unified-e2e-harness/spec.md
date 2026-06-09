## MODIFIED Requirements

### Requirement: Mode-selectable execution

The harness SHALL accept a `--mode local|kubernetes` flag that selects the deployment substrate for every scenario. In `local` mode it MUST drive ncps via `dev-scripts/run.py` (fixed dev ports). In `kubernetes` mode it MUST deploy ncps onto a Kind cluster via the existing Helm chart. A scenario definition SHALL be substrate-agnostic: the same scenario MUST run unchanged in either mode whenever the requested topology is expressible in that mode. The feature-behavior phase drivers (`serve`, `cdc-lifecycle`, `staging-contention`) MUST bind only to the substrate-agnostic `Deployment` protocol, and `kubernetes` mode MUST provide a `Deployment` implementation so a driver runs unchanged on Kind — a scenario whose topology AND timing the mode CAN express MUST NOT be reported as SKIPPED merely because the substrate lacks an adapter. (A scenario MAY still pin itself to a mode when the *other* mode cannot satisfy its assertions — e.g. `staging-contention` stays `local`-only because port-forward jitter makes single-shot in-flight activation unreliable on Kind.)

#### Scenario: Local mode drives run.py

- **WHEN** the harness is invoked with `--mode local --scenario <name>`
- **THEN** it launches the scenario's ncps instance(s) through `dev-scripts/run.py` against the fixed-port `nix run .#deps` backends and runs the scenario's phases to completion

#### Scenario: Kubernetes mode drives Kind and Helm

- **WHEN** the harness is invoked with `--mode kubernetes --scenario <name>`
- **THEN** it provisions (or reuses) a Kind cluster, installs ncps via the Helm chart with the scenario's values, and runs the same scenario phases against the cluster

#### Scenario: Mode is required and validated

- **WHEN** the harness is invoked without `--mode`, or with a value other than `local` or `kubernetes`
- **THEN** it exits non-zero with a usage error and runs no scenario

#### Scenario: Topology unsupported in the selected mode is skipped explicitly

- **WHEN** a scenario requires a topology the selected mode cannot express (e.g. an external-secret or anti-affinity scenario, which only `kubernetes` mode provides, requested with `--mode local`)
- **THEN** the harness reports the scenario as SKIPPED with the reason, and does not report it as PASSED

#### Scenario: Phase-driver scenarios run unchanged in kubernetes via the adapter

- **WHEN** a phase-driver scenario whose topology AND timing kubernetes can satisfy (e.g. `cdc-lifecycle`) is invoked with `--mode kubernetes`
- **THEN** the harness runs the identical phase driver through the kubernetes `Deployment` adapter and reports a real PASS/FAIL, never SKIPPING it for lack of a kubernetes substrate

### Requirement: CDC lifecycle scenario

The harness SHALL drive the full content-defined-chunking lifecycle `non-CDC → CDC (eager + lazy) → drain → non-CDC`, asserting both serving correctness and database invariants at each phase, in both modes. In `local` mode this is the single-instance `cdc-lifecycle` scenario. In `kubernetes` mode the same `cdc-lifecycle` phase driver MUST run unchanged through the kubernetes `Deployment` adapter; the multi-replica `cdc-lifecycle` permutation MUST additionally exercise the topology behaviors (cross-replica presence consistency, storage-lag tolerance, and chunk-store auto-derivation) that a single process cannot observe. The two catalog entries share the lifecycle phases; the substrate-specific operations (CDC enable/disable toggle, restart, `migrate-chunks-to-nar`, and DB-invariant access) go through each mode's adapter, and the kubernetes adapter MUST be able to enable CDC (eager and lazy), not only disable it.

#### Scenario: CDC-off baseline serves whole-file NARs

- **WHEN** the scenario pushes and serves a NAR with CDC disabled
- **THEN** the NAR is stored as a whole file and served byte-identical to the canonical store-path NAR, and no chunk rows exist

#### Scenario: Enabling CDC chunks NARs and normalizes narinfo

- **WHEN** CDC is enabled and the NAR is read
- **THEN** eager and lazy chunking store chunk sequences, the served narinfo is normalized, and the served NAR remains byte-identical to the canonical NAR

#### Scenario: Disabling CDC enters drain mode and migrate-chunks-to-nar drains it

- **WHEN** CDC is disabled while chunked `nar_file` rows remain and `ncps migrate-chunks-to-nar` is run
- **THEN** the instance keeps a chunk store alive (drain mode) until every chunked NAR is rewritten as a whole file, serving correctly throughout

#### Scenario: Restart after drain clears stored CDC config

- **WHEN** the instance is restarted with zero chunked NARs remaining
- **THEN** `initCDCDrainMode` clears the stored CDC config and the instance starts without a chunk store

#### Scenario: Kubernetes mode exercises multi-replica topology

- **WHEN** the multi-replica `cdc-lifecycle` permutation runs in `--mode kubernetes`
- **THEN** NAR presence agrees across replicas, reads after a cross-replica write tolerate storage lag, and the chunk store is present during CDC and absent after drain on each replica

#### Scenario: Kubernetes adapter toggles CDC and reads DB invariants in-cluster

- **WHEN** the `cdc-lifecycle` driver runs in `--mode kubernetes` and needs to enable CDC, then later assert chunk-row invariants
- **THEN** the kubernetes `Deployment` adapter enables CDC (eager and lazy) via a config toggle and rollout, and the driver reads the per-dialect DB invariants in-cluster (sqlite via pod exec, postgres/mysql via port-forward)

### Requirement: In-flight staging contention scenario

The harness SHALL provide a `staging-contention` scenario that proves in-flight NAR staging activates under real multi-replica contention and delivers complete, byte-identical NARs. The scenario MUST launch at least two replicas with a Redis distributed locker and staging enabled, race concurrent clients fetching the same uncached NAR so that lock-losing waiters become staging consumers, and FAIL if staging never activates. It MUST cover both the download window (CDC off) and the chunking window (CDC on) as independently-scored runs. This scenario SHALL remain `local`-mode only: in-flight staging *activation* is a single-shot timing event, and reaching `kubernetes` replicas through `kubectl port-forward` introduces per-request latency jitter that de-synchronizes the race so the lock-holder caches the NAR before cross-pod waiters can contend on an in-flight piece; activation therefore cannot be reliably forced on Kind. The harness MUST report the scenario as SKIPPED (never PASSED) when requested in `kubernetes` mode.

#### Scenario: Concurrent same-NAR fetch activates staging

- **WHEN** at least two replicas run with `--locker redis` and staging enabled and N clients race to fetch the same large uncached NAR
- **THEN** at least one lock-losing waiter serves from committed staging parts, evidenced by the staging-activation log line

#### Scenario: All racing readers receive identical complete NARs

- **WHEN** the racing clients complete their fetches
- **THEN** every reader receives a NAR whose decompressed content is byte-identical to the canonical store-path NAR and to every other reader, with a truncated or differing body failing even on HTTP 200

#### Scenario: Non-activation is a failure, not a pass

- **WHEN** a run completes without staging ever activating
- **THEN** the harness reports the scenario as FAILED with diagnostics, not as PASSED

#### Scenario: Both protected windows are covered

- **WHEN** the scenario is run
- **THEN** it exercises the download window (CDC off, whole-file NARs) and the chunking window (CDC on) as separate runs each with its own pass/fail

#### Scenario: Kubernetes mode skips the scenario rather than running it unreliably

- **WHEN** the `staging-contention` scenario is requested with `--mode kubernetes`
- **THEN** the harness reports it as SKIPPED (topology/timing unsupported in that mode), never PASSED, because port-forward jitter makes single-shot in-flight activation unreliable on Kind
