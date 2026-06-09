## REMOVED Requirements

### Requirement: Local CDC lifecycle e2e driver
**Reason**: The standalone local driver (`test-cdc-lifecycle-e2e.py`) and its fixed-port wrapper are replaced by the `cdc-lifecycle` scenario of the `unified-e2e-harness`.
**Migration**: Run `task test:e2e -- --mode local --scenario cdc-lifecycle` (or `nix run .#e2e -- --mode local --scenario cdc-lifecycle`).

### Requirement: CDC-off baseline phase
**Reason**: Absorbed into the `cdc-lifecycle` scenario's CDC-off baseline phase in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario; no separate invocation.

### Requirement: CDC-on chunking and narinfo normalization phase
**Reason**: Absorbed into the `cdc-lifecycle` scenario's CDC-on phase in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario; no separate invocation.

### Requirement: CDC-disable drain phase
**Reason**: Absorbed into the `cdc-lifecycle` scenario's drain phase in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario; no separate invocation.

### Requirement: Restart drain auto-completion phase
**Reason**: Absorbed into the `cdc-lifecycle` scenario's restart phase in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario; no separate invocation.

### Requirement: Cross-cutting lifecycle invariants
**Reason**: Absorbed into the `cdc-lifecycle` scenario's per-phase serving and database invariant assertions in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `cdc-lifecycle` scenario; no separate invocation.
