## Context

Three e2e harnesses exist today, all in Python:

- `nix/k8s-tests/` — a `writeShellApplication` (`packages.k8s-tests`) wrapping `src/k8s_tests.py` (CLI: `cluster create|destroy|info`, `generate`, `install`, `test`, `cleanup`, `all`) and `src/k8s_tests_tester.py` (`NCPSTester` assertions). Scenarios are a Nix attrset in `config.nix` (13 permutations: 7 single-instance, 2 external-secret, 3 HA) that the CLI reads via `CONFIG_FILE` to produce Helm values.
- `dev-scripts/test-cdc-lifecycle-e2e.py` and `dev-scripts/test-inflight-staging-contention-e2e.py` — standalone local drivers that drive ncps through `dev-scripts/run.py` (flags `--replicas`, `--db`, `--storage`, `--locker`, `--enable-cdc`, `--enable-lazy-cdc`, `--inflight-staging`; writes `var/ncps/state.json`). Each is fronted by a fixed-port `*-auto.sh` wrapper and a `task test:*` target.

The two local drivers re-implement the dependency lifecycle, port management, and the storage×DB×replica×CDC matrix that `k8s-tests` already encodes, yet they actively *drive* feature behaviors (CDC drain lifecycle; contention-activated staging) that `k8s-tests` only configures. And `k8s-tests` runs only against Kubernetes, so none of its permutations can be reproduced locally for fast iteration.

The specs (`unified-e2e-harness`) require one scenario-driven harness with `--mode local|kubernetes`, a declarative catalog, shared dependency lifecycle, `task`/`nix run` entrypoints, and the `cdc-lifecycle` and `staging-contention` behaviors as scenarios. The three old capabilities are REMOVED and absorbed.

## Goals / Non-Goals

**Goals:**

- One Python harness with a `--mode local|kubernetes` flag and a single declarative scenario catalog that is the sole source of truth for both modes.
- A mode-adapter abstraction so phase drivers (CDC lifecycle, staging contention, plain serve checks) are written once and run against either substrate.
- Preserve every assertion the three harnesses make today (byte-identical NAR delivery, CDC drain DB invariants, staging activation proof, k8s topology checks) — this is a consolidation, not a behavior change.
- Expose `task test:e2e` and `nix run .#e2e` over one implementation; remove the old scripts, wrappers, Taskfile targets, and `nix/k8s-tests`.

**Non-Goals:**

- No change to ncps runtime, CDC, staging, the Helm chart, Kind topology, or supported backends.
- Not adding tested behaviors beyond what the three harnesses cover today.
- Not unifying with the Go unit/integration suite (`task test`) or `dev-scripts/test-auto.sh` (random-port Go runner) — those stay as-is.

## Decisions

### D1: Python, one consolidated package — not Go, not extend k8s-tests in place

All three harnesses are already Python; `run.py`, `NCPSTester`, and both drivers contain reusable NAR-fetch / byte-compare / DB-invariant / staging-activation logic. The harness lives in a new directory (proposed `nix/e2e-tests/`, mirroring `nix/k8s-tests/`) with a `src/` Python package and a `flake-module.nix` exposing `packages.e2e` + `apps.e2e`.

- **Alternative — rewrite in Go**: rejected. No reuse, and the work is process orchestration (subprocess, kubectl/helm, HTTP), which Python already does here.
- **Alternative — graft modes onto `k8s_tests.py`**: rejected. Its CLI and Nix-driven value generation are Kubernetes-shaped; bolting a local mode on keeps the Nix-only catalog coupling. A clean catalog + adapter split is cheaper than retrofitting.

### D2: Keep `config.nix` as the single catalog, extended with local-mode fields

`config.nix` stays the source of truth. The harness already reads it the right way: `k8s_tests.py` does `nix eval --json --file config.nix permutations` (k8s_tests.py:616) — one eval at startup that materializes the whole catalog as JSON, which the Python loads. The local adapter consumes the same materialized JSON, so there is one catalog for both modes and no parallel format to keep in sync. `config.nix` moves to `nix/e2e-tests/config.nix` when `nix/k8s-tests/` is removed; its `generateValues` → Helm-values path is preserved unchanged.

Each permutation entry is extended additively with the fields the local adapter and phase drivers need: `phase` (`serve` | `cdc-lifecycle` | `staging-contention`, default `serve`), `modes` (subset of `local`/`kubernetes`, default both where topology allows), and explicit `cdc` (off|eager|lazy) / `staging` (bool) where not already implied by the existing `features`/`inflightStaging` keys. The existing `generateValues` reads only the keys it already knows, so new keys are inert on the Kubernetes path.

- **Alternative — language-neutral YAML/Python catalog**: rejected. It would duplicate the 13 permutations already encoded in `config.nix` and force re-implementing the `config.nix` → Helm-values generation in Python, risking drift from the chart for no gain. Nix is flexible here and already JSON-materialized.

### D3: A `Deployment` adapter interface with two implementations

```
class Deployment(Protocol):
    def provision(self, scenario) -> None        # bring ncps up for this scenario
    def replica_urls(self) -> list[str]          # base URLs of each ncps replica
    def restart(self, *, with_cdc: bool) -> None # stop/restart with changed flags (drain lifecycle)
    def run_subcommand(self, args) -> int        # e.g. migrate-chunks-to-nar
    def db(self) -> DBAccess                      # query nar_file / config rows
    def logs(self, replica) -> str                # for staging-activation assertions
    def teardown(self) -> None                    # always called, success or failure
```

- `LocalDeployment` drives `dev-scripts/run.py` (fixed dev ports, `go run`, `state.json`), reads DB over the dev DB URLs, reads logs from `var/log/ncps-<port>.log`.
- `KubernetesDeployment` drives Kind + Helm (reusing `k8s_tests.py` cluster/install logic), reaches replicas and the DB via `kubectl port-forward`, reads logs via `kubectl logs`.

Phase drivers depend only on `Deployment` + a shared `Client` (push/serve NAR, byte-compare against `nix-store --dump`) and `DBAccess`. `restart(with_cdc=...)` is the seam the CDC lifecycle needs; the local adapter restarts the process with different `run.py` flags, the k8s adapter patches the release/Deployment and waits for rollout.

- **Alternative — two parallel phase implementations per mode**: rejected; defeats the consolidation and risks assertion drift.

### D4: Topology capability is declared per scenario; unsupported (mode, scenario) pairs SKIP, never PASS

Each catalog entry lists `modes`. Local mode can run multi-replica (run.py `--replicas>1`) but cannot express anti-affinity, PDB, external-secret, or `migration.mode=job`; those scenarios are `modes: [kubernetes]`. The runner reports an explicit SKIP with reason when a requested (mode, scenario) is unsupported (spec: "Topology unsupported in the selected mode is skipped explicitly").

### D5: Entrypoints — `apps.e2e` + `task test:e2e`, sharing one CLI

`nix run .#e2e -- --mode <m> --scenario <s>` runs the `writeShellApplication` (runtime inputs: kubectl, helm, kind, skopeo, docker, `python3.withPackages [boto3 brotli kubernetes psycopg2 pymysql pyyaml requests zstandard]`, plus the local-mode toolchain available from the devshell). `task test:e2e -- ...` forwards `{{.CLI_ARGS}}` to the same entrypoint. `--list` prints the catalog. The old `task test:cdc-lifecycle` and `task test:inflight-staging-contention` targets and both `*-auto.sh` wrappers are removed; their fixed-port dependency lifecycle moves into the local adapter.

### D6: The harness stays out of the per-PR hot path

Like the harnesses it replaces, the unified harness is **manual / opt-in** and is **NOT** added to `nix flake check`. Today nothing it consolidates runs per-PR: the Kind `k8s-tests` harness and both local drivers are manual (`.github/workflows/` references none of them); only fast Helm *unit* tests and Go checks run in `nix flake check`. That stays true — `nix flake check` must not gain any scenario whose wall-clock exceeds **3 minutes**, and the Kind/network-NAR scenarios never will.

If automated coverage is wanted, it goes on a **scheduled (nightly) workflow** (mirroring `devskim.yml`/`fuzz.yml`'s `schedule:` trigger), not on PRs. A scenario may only be promoted into `nix flake check` if it is individually proven to run under 3 minutes; that is an explicit per-scenario decision, not the default.

- **Alternative — repoint the existing per-backend `nix flake check` derivations at the harness**: rejected. Those derivations (`ncps-*-tests`) are fast Go integration tests, unrelated to this Python e2e harness, and are out of scope here; coupling the heavy harness into them would slow every PR.

## Risks / Trade-offs

- **R1 — Behavior/assertion drift during port** → Reproduce each known-good run with the new harness before deleting the old scripts: drive `cdc-lifecycle` and `staging-contention` in local mode and confirm identical PASS/FAIL semantics (including the "non-activation is a FAILURE" guard); only then remove the originals. Build via TDD per repo rules.
- **R2 — Extending `config.nix` entries breaks the existing `generateValues` Helm path** → Add only additive keys (`phase`, `modes`, `cdc`, `staging`); `generateValues` reads only the keys it already knows, so new keys are inert on the Kubernetes path. Verify every kubernetes scenario still installs unchanged after the extension.
- **R3 — Kubernetes mode is heavy/slow (Kind, image push)** → Reuse the existing cluster lifecycle and image-generation path from `k8s_tests.py`; allow cluster reuse across scenarios as today.
- **R4 — DB-invariant queries differ across sqlite/postgres/mysql** → `DBAccess` keeps a per-dialect query map (the drivers already special-case this); no cross-dialect query mixing.
- **R5 — Big-bang removal breaks a developer's muscle memory / CI** → Single migration branch that adds the harness, proves parity, then removes old paths and updates docs in the same change so nothing references a deleted script.

## Migration Plan

1. Scaffold `nix/e2e-tests/` (Python `src/`, `flake-module.nix` exposing `packages.e2e` + `apps.e2e`); the catalog continues to come from `config.nix` via `nix eval --json`. Wire `apps.e2e` and `task test:e2e`. No removals yet.
2. Implement `Deployment` interface, `LocalDeployment`, shared `Client`/`DBAccess`, and the `serve` phase. Add a trivial `serve` scenario; verify local mode green.
3. Port the `cdc-lifecycle` phase driver (lifting `test-cdc-lifecycle-e2e.py`); verify local-mode parity against the known-good lifecycle run.
4. Port the `staging-contention` phase driver (lifting `test-inflight-staging-contention-e2e.py`, both windows + activation guard); verify local-mode parity.
5. Implement `KubernetesDeployment` (reusing `k8s_tests.py` cluster/install/image logic + the existing `config.nix` `generateValues` path); extend the 13 `config.nix` permutations with the additive `phase`/`modes`/`cdc`/`staging` keys; verify `--mode kubernetes` reproduces `k8s-tests`, including `cdc-lifecycle` on a multi-replica cluster.
6. Keep the harness manual/opt-in (NOT in `nix flake check`). Optionally add a nightly scheduled workflow that runs selected scenarios; only promote an individual scenario into `nix flake check` if it is proven < 3 min.
7. Move `config.nix` to `nix/e2e-tests/config.nix`, then **remove**: `nix/k8s-tests/` (and its `flake-module.nix` import), `dev-scripts/test-cdc-lifecycle-auto.sh`, `dev-scripts/test-inflight-staging-contention-auto.sh`, `dev-scripts/test-cdc-lifecycle-e2e.py`, `dev-scripts/test-inflight-staging-contention-e2e.py`, and the `task test:cdc-lifecycle` / `test:inflight-staging-contention` targets.
8. Update docs: rewrite the "Helm Chart Testing" section of `docs/docs/Developer Guide/Contributing.md`, add the unified entrypoint to `docs/docs/Developer Guide/Testing.md`, and replace `nix/k8s-tests/README.md` with `nix/e2e-tests/README.md`.

**Rollback**: the removals land as one commit (step 7); reverting it restores `nix/k8s-tests`, both wrappers, and both drivers, since the harness was added additively in steps 1–6.

## Open Questions

_All resolved before apply:_

- **Catalog format** — keep `config.nix` (already JSON-materialized via `nix eval`), extended additively.
- **CI placement** — harness stays out of `nix flake check`; nothing is promoted unless proven < 3 min.
- **Harness location** — `nix/e2e-tests/` (mirrors `nix/k8s-tests/`, keeps `config.nix` beside it).
- **`run.py` flags** — local adapter composes existing `run.py` flags; a new flag is added only if a phase cannot otherwise be expressed (decided during implementation).
- **Nightly workflow** — deferred to a follow-up; this change ships the harness manual-only (task 8.2 is optional).
