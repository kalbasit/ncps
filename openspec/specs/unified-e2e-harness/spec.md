# unified-e2e-harness

## Purpose

One scenario-driven end-to-end harness that runs a declarative scenario catalog
against either a local `dev-scripts/run.py` deployment or a Kind/Helm Kubernetes
deployment, selected with `--mode`. It consolidates the former `nix/k8s-tests`
CLI and the standalone `dev-scripts/test-cdc-lifecycle-e2e.py` /
`dev-scripts/test-inflight-staging-contention-e2e.py` drivers — absorbing the
CDC-lifecycle, in-flight-staging-contention, and Helm permutation behaviors as
scenarios — behind a single `task test:e2e` / `nix run .#e2e` entrypoint.

## Requirements

### Requirement: Mode-selectable execution

The harness SHALL accept a `--mode local|kubernetes` flag that selects the deployment substrate for every scenario. In `local` mode it MUST drive ncps via `dev-scripts/run.py` (fixed dev ports). In `kubernetes` mode it MUST deploy ncps onto a Kind cluster via the existing Helm chart. A scenario definition SHALL be substrate-agnostic: the same scenario MUST run unchanged in either mode whenever the requested topology is expressible in that mode.

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

### Requirement: Declarative scenario catalog

The harness SHALL define its scenarios declaratively in a single catalog. Each scenario MUST specify its dimensions — storage backend, database, replica count, CDC enabled/disabled, in-flight staging enabled/disabled — and the feature-behavior phase driver it runs (e.g. `cdc-lifecycle`, `staging-contention`, or a plain serve/health check). Scenarios MUST be addressable by a stable kebab-case name. The catalog MUST be the single source of truth for both modes; adding a scenario MUST NOT require editing per-mode code.

#### Scenario: Scenarios are discoverable

- **WHEN** the harness is invoked with `--list` (or an equivalent listing subcommand)
- **THEN** it prints every catalog scenario name together with its dimensions and the modes it supports

#### Scenario: A scenario is runnable by name

- **WHEN** the harness is invoked with `--scenario <name>` for a catalog entry
- **THEN** it runs exactly that scenario and no other

#### Scenario: Unknown scenario name fails fast

- **WHEN** `--scenario <name>` names an entry absent from the catalog
- **THEN** the harness exits non-zero with an error listing valid scenario names

### Requirement: Dependency lifecycle and result reporting

The harness SHALL own the lifecycle of the backing dependencies for the selected mode and report results uniformly. In `local` mode it MUST start the fixed-port backends (`nix run .#deps`: S3/Garage, PostgreSQL, MariaDB, Redis) when they are not already running and stop the ones it started on exit. In `kubernetes` mode it MUST provision the cluster dependencies. It MUST report per-scenario and per-phase PASS/FAIL and MUST exit non-zero if any scenario or phase fails.

#### Scenario: Dependencies are started and torn down

- **WHEN** the harness runs a scenario that needs backends it had to start
- **THEN** the required services are confirmed reachable before the scenario runs, and the services the harness started are stopped on exit (success or failure)

#### Scenario: Failure produces a non-zero exit

- **WHEN** any scenario phase asserts a failure (incomplete NAR, wrong DB invariant, missing activation, etc.)
- **THEN** the harness reports that phase as FAILED and the overall process exits non-zero

#### Scenario: Resources are cleaned up on failure

- **WHEN** a scenario aborts mid-run
- **THEN** the harness still tears down the ncps instances and the dependencies it started, leaving no orphaned processes (local) or installs (kubernetes)

### Requirement: task and nix run entrypoints

The harness SHALL be invocable through `task` and through `nix run`. `task test:e2e -- --mode <mode> --scenario <name>` MUST forward its arguments to the harness, and `nix run .#e2e -- --mode <mode> --scenario <name>` MUST run the equivalent. These two entrypoints MUST share one implementation and one scenario catalog.

#### Scenario: task entrypoint forwards arguments

- **WHEN** a developer runs `task test:e2e -- --mode local --scenario cdc-lifecycle`
- **THEN** the harness runs the `cdc-lifecycle` scenario in local mode with the same behavior as a direct invocation

#### Scenario: nix run entrypoint is equivalent

- **WHEN** a developer runs `nix run .#e2e -- --mode kubernetes --scenario ha-s3-postgres`
- **THEN** the harness runs that scenario in kubernetes mode identically to the `task` entrypoint

### Requirement: CDC lifecycle scenario

The harness SHALL drive the full content-defined-chunking lifecycle `non-CDC → CDC (eager + lazy) → drain → non-CDC`, asserting both serving correctness and database invariants at each phase, in both modes. In `local` mode this is the single-instance `cdc-lifecycle` scenario. In `kubernetes` mode it is a multi-replica `cdc-lifecycle` permutation that MUST additionally exercise the topology behaviors (cross-replica presence consistency, storage-lag tolerance, and chunk-store auto-derivation) that a single process cannot observe. The two catalog entries share the lifecycle phases; the substrate-specific operations (restart, `migrate-chunks-to-nar`) go through each mode's adapter.

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

### Requirement: In-flight staging contention scenario

The harness SHALL provide a `staging-contention` scenario that proves in-flight NAR staging activates under real multi-replica contention and delivers complete, byte-identical NARs. The scenario MUST launch at least two replicas with a Redis distributed locker and staging enabled, race concurrent clients fetching the same uncached NAR so that lock-losing waiters become staging consumers, and FAIL if staging never activates. It MUST cover both the download window (CDC off) and the chunking window (CDC on) as independently-scored runs.

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

### Requirement: Storage and database backend matrix

The harness SHALL let a scenario select its storage backend (`local` shared path or `s3`) and database (`sqlite`, `postgres`, `mysql`/MariaDB), subject to the selected mode's topology constraints. Multi-replica scenarios MUST use a shared storage backend and a non-SQLite shared database. The kubernetes mode MUST be able to express the deployment permutations previously covered by `nix/k8s-tests` (single-instance storage×DB combinations, external-secret variants, and HA multi-replica combinations) as catalog scenarios.

#### Scenario: Backend is selectable per scenario

- **WHEN** a scenario declares `storage: s3` and `database: postgres`
- **THEN** the harness configures ncps for S3 storage and PostgreSQL in the selected mode

#### Scenario: Previously k8s-only permutations exist as scenarios

- **WHEN** the catalog is listed
- **THEN** the single-instance, external-secret, and HA permutations formerly defined in `nix/k8s-tests/config.nix` are present as named scenarios runnable in `--mode kubernetes`

### Requirement: Harness stays out of the per-PR hot path

The harness SHALL be manual / opt-in and MUST NOT be added to `nix flake check`. A scenario MUST NOT be included in `nix flake check` unless its wall-clock runtime is independently proven to be under 3 minutes; Kind and network-NAR scenarios MUST remain excluded. Automated coverage, if any, SHALL run on a scheduled (e.g. nightly) workflow rather than on pull requests.

#### Scenario: Per-PR check does not run the harness

- **WHEN** `nix flake check` runs on a pull request
- **THEN** it does not invoke any unified-harness scenario that runs a Kind cluster or pulls NARs over the network

#### Scenario: Promotion requires a proven sub-3-minute runtime

- **WHEN** someone proposes adding a harness scenario to `nix flake check`
- **THEN** it is admitted only if that scenario's wall-clock runtime is shown to be under 3 minutes, otherwise it stays manual or moves to a scheduled workflow
