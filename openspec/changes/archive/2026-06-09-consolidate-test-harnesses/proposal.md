## Why

We now have three overlapping end-to-end harnesses: `nix/k8s-tests` (13 Kind/Helm permutations), `test-cdc-lifecycle-e2e.py`, and `test-inflight-staging-contention-e2e.py` (each with its own fixed-port `*-auto.sh` wrapper). The two Python drivers duplicate boilerplate already in `k8s-tests` (storage × DB × replica × CDC matrix, dependency lifecycle, port management) yet exercise feature behaviors (CDC drain lifecycle, contention-activated staging) that `k8s-tests` only configures but never actively drives. Meanwhile `k8s-tests` can only run against Kubernetes, so the same scenario cannot be reproduced locally for fast iteration. Three diverging harnesses mean every new feature behavior gets tested inconsistently or not at all.

## What Changes

- Introduce a single scenario-driven e2e harness with a `--mode local|kubernetes` flag. `local` drives ncps via `dev-scripts/run.py`; `kubernetes` deploys onto a Kind cluster via the existing Helm chart.
- Define scenarios declaratively (storage backend, database, replicas, CDC on/off, staging on/off, plus a feature-behavior phase script such as CDC lifecycle or staging contention). The same scenario runs identically in either mode where the topology supports it.
- Fold the CDC lifecycle and in-flight staging contention behaviors into the scenario catalog as reusable phase drivers instead of standalone scripts.
- Expose the harness through `task` (e.g. `task test:e2e -- --mode local --scenario cdc-lifecycle`) and an equivalent `nix run .#e2e`, matching existing `k8s-tests`/`task` conventions.
- **BREAKING**: Replace `nix/k8s-tests` with the unified harness. The `k8s-tests` CLI/entrypoint and the 13 named permutations are superseded by harness scenarios.
- **BREAKING**: Remove `test-cdc-lifecycle-auto.sh`, `test-inflight-staging-contention-auto.sh`, and their `*-e2e.py` drivers; replace the `task test:*` targets with unified-harness invocations.

## Capabilities

### New Capabilities
- `unified-e2e-harness`: one scenario-driven harness that runs a declarative scenario catalog against either a local `run.py` deployment or a Kind/Helm Kubernetes deployment via a `--mode` flag, with shared dependency-lifecycle and result reporting. This single capability absorbs the CDC lifecycle, in-flight staging contention, and k8s permutation behaviors as scenarios.

### Modified Capabilities
- `cdc-lifecycle-e2e`: **REMOVED** — its `non-CDC → CDC → drain → non-CDC` lifecycle behavior is absorbed into `unified-e2e-harness` as the `cdc-lifecycle` scenario.
- `inflight-staging-contention-e2e`: **REMOVED** — its contention-activated staging behavior is absorbed into `unified-e2e-harness` as the `staging-contention` scenario.
- `cdc-lifecycle-k8s-test`: **REMOVED** — its k8s CDC lifecycle dimension is absorbed into `unified-e2e-harness` `kubernetes` mode.

## Impact

- Code: `nix/k8s-tests/` (replaced), `dev-scripts/test-cdc-lifecycle-*`, `dev-scripts/test-inflight-staging-*`, `dev-scripts/run.py` (scenario hooks), `Taskfile.yml` test targets, `nix/k8s-tests/README.md`.
- Tooling: CI `nix flake check` derivations that invoke `k8s-tests` and the affected `task` targets.
- Docs: `docs/docs/Developer Guide/Contributing.md` (the "Helm Chart Testing" section is a full `k8s-tests` CLI walkthrough that must be rewritten for `task test:e2e` / `nix run .#e2e` and the scenario model), `docs/docs/Developer Guide/Testing.md` (add the unified entrypoint), and `nix/k8s-tests/README.md`.
- I/O / network / memory: no change to ncps runtime behavior — harness-only. Local mode keeps the existing fixed-port footprint; Kubernetes mode keeps the Kind cluster footprint. Consolidation removes duplicate dependency stacks spun up per driver.

## Non-goals

- No changes to ncps production code, CDC, or staging behavior.
- Not adding new tested feature behaviors beyond what the three harnesses cover today.
- Not changing the Helm chart, Kind topology, or supported storage/DB backends.
- Not replacing the Go unit/integration suite (`task test`) or `test-auto.sh`.
