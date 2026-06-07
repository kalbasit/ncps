## ADDED Requirements

### Requirement: CDC lifecycle k8s-tests dimension

The `nix/k8s-tests` harness SHALL gain a new lifecycle permutation/dimension (defined in `nix/k8s-tests/config.nix`, not a separate framework) that deploys ncps on a multi-replica Kind cluster and runs the same `non-CDC → CDC → drain → non-CDC` phases as the local driver, in order to exercise topology behaviors that a single-process test cannot observe. The permutation MUST be discoverable and runnable through the existing `k8s-tests` CLI (`generate`, `install`, `test`).

#### Scenario: New permutation is listed and runnable

- **WHEN** `k8s-tests` lists available permutations after regeneration
- **THEN** the new CDC-lifecycle permutation appears and can be installed and tested via the existing CLI verbs

#### Scenario: Lifecycle phases run on the cluster

- **WHEN** the CDC-lifecycle permutation test runs
- **THEN** it executes the CDC-off baseline, CDC-on chunking, CDC-disable drain, and restart auto-completion phases against the deployed cluster and fails the test on any phase assertion failure

### Requirement: Drain auto-exit on pod restart

The k8s lifecycle test SHALL verify that, once all chunked NARs are drained, restarting a pod causes `initCDCDrainMode` to auto-complete so the pod no longer runs in drain mode.

#### Scenario: Restarted pod exits drain mode

- **WHEN** chunked NARs have been fully drained and a pod is deleted/restarted
- **THEN** the new pod boots with the stored CDC config cleared, initializes no chunk store, and is not in drain mode

### Requirement: Multi-replica shared-DB presence consistency

With multiple replicas sharing one database, the k8s lifecycle test SHALL verify that NAR presence (HEAD/GET) is consistent across replicas during and after lifecycle transitions.

#### Scenario: Presence agrees across replicas

- **WHEN** a NAR is pushed to one replica and queried via another replica during CDC and drain phases
- **THEN** both replicas report presence consistent with the shared database and actual stored bytes, with no phantom presence

### Requirement: Storage lag tolerance

The k8s lifecycle test SHALL exercise the topology over shared/networked storage so that propagation lag between a write and a subsequent read on another replica does not produce phantom-presence or missing-reference failures.

#### Scenario: Read after cross-replica write tolerates lag

- **WHEN** a replica writes NAR bytes and another replica serves a request that depends on them shortly after
- **THEN** the serving replica either returns the bytes or treats them as absent consistently with the DB, never returning a 200 HEAD with absent bytes

### Requirement: Chunk-store auto-derivation under topology

The k8s lifecycle test SHALL verify that the chunk store is auto-derived correctly across replicas when CDC is enabled and absent after drain completion.

#### Scenario: Chunk store present during CDC, absent after drain

- **WHEN** CDC is enabled, every replica derives a chunk store and serves chunked NARs; and after drain completes and pods restart
- **THEN** no replica initializes a chunk store and chunked-serving code paths are no longer reachable
