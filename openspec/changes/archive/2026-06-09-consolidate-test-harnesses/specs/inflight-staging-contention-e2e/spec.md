## REMOVED Requirements

### Requirement: Contention-activated in-flight staging driver
**Reason**: The standalone driver (`test-inflight-staging-contention-e2e.py`) and its fixed-port wrapper are replaced by the `staging-contention` scenario of the `unified-e2e-harness`.
**Migration**: Run `task test:e2e -- --mode local --scenario staging-contention` (or `nix run .#e2e -- --mode local --scenario staging-contention`).

### Requirement: Complete byte-identical NAR delivery under contention
**Reason**: Absorbed into the `staging-contention` scenario's byte-identical delivery assertions in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `staging-contention` scenario; no separate invocation.

### Requirement: Coverage of both protected windows
**Reason**: Absorbed into the `staging-contention` scenario, which runs the download window (CDC off) and chunking window (CDC on) as independently-scored runs in `unified-e2e-harness`.
**Migration**: Covered by the unified harness `staging-contention` scenario; no separate invocation.

### Requirement: Storage-backend matrix
**Reason**: Absorbed into the `unified-e2e-harness` storage and database backend matrix, which selects `local` or `s3` per scenario.
**Migration**: Select the backend via the scenario's storage dimension in the unified harness.

### Requirement: One-command fixed-port wrapper
**Reason**: The `*-auto.sh` fixed-port wrapper is replaced by the unified harness's own dependency-lifecycle management.
**Migration**: Use `task test:e2e` / `nix run .#e2e`, which start and stop the required backends.
