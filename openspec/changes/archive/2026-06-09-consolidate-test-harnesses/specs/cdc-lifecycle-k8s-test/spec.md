## REMOVED Requirements

### Requirement: CDC lifecycle k8s-tests dimension
**Reason**: The dedicated `nix/k8s-tests` CDC lifecycle dimension is replaced by running the `unified-e2e-harness` `cdc-lifecycle` scenario in `--mode kubernetes`.
**Migration**: Run `task test:e2e -- --mode kubernetes --scenario cdc-lifecycle` (or `nix run .#e2e -- --mode kubernetes --scenario cdc-lifecycle`).

### Requirement: Drain auto-exit on pod restart
**Reason**: Absorbed into the `cdc-lifecycle` scenario's restart phase, exercised under the kubernetes multi-replica topology in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario in kubernetes mode.

### Requirement: Multi-replica shared-DB presence consistency
**Reason**: Absorbed into the `cdc-lifecycle` scenario's kubernetes multi-replica topology assertions in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario in kubernetes mode.

### Requirement: Storage lag tolerance
**Reason**: Absorbed into the `cdc-lifecycle` scenario's kubernetes multi-replica topology assertions in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario in kubernetes mode.

### Requirement: Chunk-store auto-derivation under topology
**Reason**: Absorbed into the `cdc-lifecycle` scenario's kubernetes multi-replica topology assertions in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario in kubernetes mode.
