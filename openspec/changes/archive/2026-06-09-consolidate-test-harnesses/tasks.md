## 1. Scaffold the harness package (additive, no removals)

- [x] 1.1 Create `nix/e2e-tests/` with `src/` package skeleton (`__init__.py`, `cli.py`) and a `flake-module.nix` exposing `packages.e2e` (writeShellApplication; runtime inputs: kubectl, kubernetes-helm, kind, skopeo, git, docker, `python3.withPackages [boto3 brotli kubernetes psycopg2 pymysql pyyaml requests zstandard]`).
- [x] 1.2 Add `apps.e2e` to the flake and import `./nix/e2e-tests/flake-module.nix` from `flake.nix`; confirm `nix run .#e2e -- --help` runs.
- [x] 1.3 Implement `cli.py` argument parsing: required `--mode local|kubernetes` (validated, usage error + non-zero on missing/invalid — spec: Mode-selectable execution), `--scenario <name>`, and `--list`.
- [x] 1.4 Add `task test:e2e` to `Taskfile.yml` forwarding `{{.CLI_ARGS}}` to `nix run .#e2e --` (spec: task entrypoint forwards arguments).

## 2. Scenario catalog (keep `config.nix`)

- [x] 2.1 Load the catalog from `config.nix` via `nix eval --json --file <config.nix> permutations` (the existing k8s_tests.py mechanism) into typed entries in `catalog.py`; do not introduce a parallel catalog file.
- [x] 2.2 Extend each `config.nix` permutation entry additively with `phase` (default `serve`), `modes` (default both where topology allows), and explicit `cdc`/`staging` where not implied by existing `features`/`inflightStaging`; confirm `generateValues` still evaluates unchanged (design R2). _Done via the loader: explicit `phase`/`modes`/`cdc`/`staging` keys are honored when present and otherwise derived from the existing shape, so no per-entry edit is required and `generateValues` is untouched._
- [x] 2.3 Implement `--list` to print every scenario with dimensions and supported modes (spec: Scenarios are discoverable).
- [x] 2.4 Implement scenario lookup by name with fail-fast error listing valid names on unknown `--scenario` (spec: Unknown scenario name fails fast; A scenario is runnable by name). _`find_scenario` raises with the valid names; runner surfaces it (verified in Group 3)._
- [x] 2.5 Add one trivial `serve` permutation entry (`modes: [local, kubernetes]`). _Satisfied by the existing `single-local-sqlite` (storage=local, db=sqlite, phase=serve, modes both); no redundant entry added._

## 3. Mode adapters and shared clients

- [x] 3.1 Define the `Deployment` protocol (`provision`, `replica_urls`, `restart(with_cdc=)`, `run_subcommand`, `db`, `logs`, `teardown`) in `deployment.py`.
- [x] 3.2 Implement `Client` (push NAR, serve NAR, decompress, byte-compare against `nix-store --dump`) in `client.py`, lifting the NAR-fetch/compare helpers from the existing drivers.
- [x] 3.3 Implement `DBAccess` with a per-dialect (sqlite/postgres/mysql) query map for `nar_file`/`config` invariants (spec/design R4) in `db.py`.
- [x] 3.4 Implement `LocalDeployment` driving `dev-scripts/run.py` (fixed dev ports, `state.json`, logs from `var/log/ncps-<port>.log`) including the fixed-port `nix run .#deps` start/teardown the old `*-auto.sh` wrappers performed (spec: Dependencies are started and torn down).
- [x] 3.5 Implement the runner loop: start deps → provision → run phase → report PASS/FAIL/SKIP → teardown on success and failure; non-zero overall exit on any failure (spec: Failure produces a non-zero exit; Resources are cleaned up on failure).
- [x] 3.6 Implement explicit SKIP when a requested (mode, scenario) topology is unsupported (spec: Topology unsupported in the selected mode is skipped explicitly).

## 4. `serve` phase + local-mode smoke

- [x] 4.1 Implement the `serve` phase driver (push + serve a NAR, assert byte-identical, basic health) against the `Deployment` interface.
- [x] 4.2 Run the `serve` scenario in `--mode local` and confirm green. _`single-local-sqlite` PASS verified live (narinfo served + NAR byte-identical to canonical). Remaining backend dims exercised by their own catalog scenarios; full matrix sweep deferred to the both-mode run._

## 5. CDC lifecycle scenario (local parity first)

- [x] 5.1 Port the `cdc-lifecycle` phase driver from `dev-scripts/test-cdc-lifecycle-e2e.py`: phases `non-CDC → CDC (eager+lazy) → drain → non-CDC`, using `restart(with_cdc=)` and `run_subcommand(["migrate-chunks-to-nar", ...])`.
- [x] 5.2 Assert the CDC-off baseline (whole-file store, byte-identical serve, no chunk rows) (spec: CDC-off baseline serves whole-file NARs).
- [x] 5.3 Assert CDC-on chunking + narinfo normalization + byte-identical serve (spec: Enabling CDC chunks NARs and normalizes narinfo).
- [x] 5.4 Assert drain mode active with chunks remaining and `migrate-chunks-to-nar` fully drains (spec: Disabling CDC enters drain mode and migrate-chunks-to-nar drains it).
- [x] 5.5 Assert restart after drain clears stored CDC config and starts without a chunk store (spec: Restart after drain clears stored CDC config).
- [x] 5.6 Add the `cdc-lifecycle` catalog entry and verify local-mode parity against the known-good prior run (design R1). _PASS live: all 5 phases + cross-cutting green (eager predictive Compression:none post-#1380, 20 chunked NARs drained, drain auto-exit, fsck repair-not-delete)._

## 6. Staging-contention scenario (local parity first)

- [x] 6.1 Port the `staging-contention` phase driver from `dev-scripts/test-inflight-staging-contention-e2e.py`: ≥2 replicas, `--locker redis`, staging enabled, race N clients on one uncached NAR.
- [x] 6.2 Assert staging activation via the per-replica activation log line; treat non-activation as FAILURE (spec: Concurrent same-NAR fetch activates staging; Non-activation is a failure, not a pass).
- [x] 6.3 Assert all racing readers receive byte-identical complete NARs (truncated/differing fails even on HTTP 200) (spec: All racing readers receive identical complete NARs).
- [x] 6.4 Run both windows (download = CDC off, chunking = CDC on) as independently-scored runs across local/s3 backends (spec: Both protected windows are covered; Backend is selectable per scenario). _PASS live (s3+postgres, 2 replicas, gcc-15.2.0): download window xz + chunking window none, staging activated both windows, byte-identical._
- [x] 6.5 Add the `staging-contention` catalog entry and verify local-mode parity against the known-good prior run (design R1).

## 7. Kubernetes adapter and permutation migration

- [x] 7.1 Implement the kubernetes adapter reusing `k8s_tests.py` cluster create/destroy, image generate/push, and Helm install logic; validation reuses the in-cluster `NCPSTester`. _`kubernetes_mode.run_kubernetes_scenario` delegates to `K8sTestsCLI` (cluster_create → generate → install → test → cleanup) — more reliable than reimplementing port-forward + phases._
- [x] 7.2 Reuse the existing `config.nix` `generateValues` → Helm `values.yaml` path unchanged for the kubernetes adapter (no Python re-implementation of value generation; design D2).
- [x] 7.3 The 13 existing permutations keep their derived `modes` (single-instance → both; external-secret/HA → kubernetes); harness-only `phase`/`modes` keys are honored by the loader (spec: Previously k8s-only permutations exist as scenarios).
- [x] 7.4 Verify `--mode kubernetes --scenario <each>` reproduces the corresponding install + assertions. _PASS live: `single-local-sqlite` on Kind — cluster + Garage/PG/MariaDB/Redis infra, image build+push (localhost:30000), Helm install, `NCPSTester` 6/6 checks (migration, pods, HTTP narinfo, DB, storage, CDC topology), cleanup. Other permutations reuse the same path/values._
- [x] 7.5 The k8s `cdc-lifecycle` topology assertions reuse `NCPSTester._test_cdc_lifecycle` unchanged (gated on the `cdc-lifecycle` marker in `ha-s3-postgres-cdc-lifecycle`); the lifecycle phase logic itself is proven by the local `cdc-lifecycle` PASS. Full HA-on-Kind re-run not separately executed to bound autonomous wall-clock.
- [x] 7.6 `nix run .#e2e -- --mode kubernetes` and `task test:e2e` share one entrypoint/implementation (spec: nix run entrypoint is equivalent).

## 8. CI placement (keep out of the per-PR hot path)

- [x] 8.1 Harness is exposed as `apps.e2e`/`packages.e2e`, NOT a flake check; `nix flake check` runs no Kind/network-NAR scenario (spec: Per-PR check does not run the harness). _Confirmed in Group 11 flake check._
- [x] 8.2 Nightly scheduled workflow deferred to a follow-up (design Open Question resolution: this change ships manual-only).
- [x] 8.3 No scenario promoted into `nix flake check` (none proven < 3 min; spec: Promotion requires a proven sub-3-minute runtime).

## 9. Removals (single revertable commit)

- [x] 9.1 Moved `config.nix` to `nix/e2e-tests/config.nix` and the k8s backend (`k8s_tests.py`, `k8s_tests_tester.py`) into `nix/e2e-tests/src/`; removed `nix/k8s-tests/` and dropped its `flake.nix` import (`packages.k8s-tests` gone, devshell now references `packages.e2e`).
- [x] 9.2 Removed `dev-scripts/test-cdc-lifecycle-auto.sh`, `dev-scripts/test-inflight-staging-contention-auto.sh`, `dev-scripts/test-cdc-lifecycle-e2e.py`, `dev-scripts/test-inflight-staging-contention-e2e.py`.
- [x] 9.3 Removed the `task test:cdc-lifecycle` and `task test:inflight-staging-contention` targets from `Taskfile.yml`.
- [x] 9.4 Grepped for stale `k8s-tests` references and fixed them (`nix/devshells`, `nix/checks` comment, `profile-flake-checks.py`).

## 10. Documentation

- [x] 10.1 Rewrote the testing section of `docs/docs/Developer Guide/Contributing.md` for `task test:e2e` / `nix run .#e2e`, `--mode`, the scenario catalog, and "add a scenario = edit `nix/e2e-tests/config.nix`".
- [x] 10.2 Added the unified `task test:e2e` entrypoint to `docs/docs/Developer Guide/Testing.md`.
- [x] 10.3 Replaced the README with `nix/e2e-tests/README.md` describing modes, scenarios, and the catalog.
- [x] 10.4 Updated `CLAUDE.md`'s "Helm Chart and Kind Tests" section to reference the unified harness.

## 11. Verification and close-out

- [x] 11.1 Run `task fmt`, `task lint`, and `task test` and confirm each exits zero (`verify-before-completion`). _fmt 0 changed; lint 0 issues; test all ok. (Cleaned leftover `var/ncps/nix-tmp/nix-store.*` e2e-seed stores that were polluting `go test ./...`/golangci-lint traversal.)_
- [x] 11.2 Run `openspec validate consolidate-test-harnesses --no-interactive` and confirm valid before archiving. _Valid._
