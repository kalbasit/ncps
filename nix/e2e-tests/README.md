# ncps unified e2e test harness

One scenario-driven end-to-end harness that runs a declarative scenario catalog
against either a **local** `dev-scripts/run.py` deployment or a **Kubernetes**
Kind/Helm deployment, selected with `--mode`. It replaces the former
`nix/k8s-tests` CLI and the standalone `dev-scripts/test-cdc-lifecycle-e2e.py` /
`dev-scripts/test-inflight-staging-contention-e2e.py` drivers.

## Usage

```bash
# List every scenario with its dimensions and supported modes.
nix run .#e2e -- --list
task test:e2e -- --list

# Run a scenario locally (run.py + fixed-port `nix run .#deps` backends).
nix run .#e2e -- --mode local --scenario cdc-lifecycle
task test:e2e -- --mode local --scenario staging-contention

# Run several scenarios in one invocation (repeatable and/or comma-separated).
nix run .#e2e -- --mode local --scenario cdc-lifecycle --scenario staging-contention
nix run .#e2e -- --mode local --scenario cdc-lifecycle,staging-contention

# Run every scenario supporting the chosen mode (unsupported ones are SKIPPED).
nix run .#e2e -- --mode local --all
nix run .#e2e -- --mode kubernetes --all

# Run a scenario on a Kind cluster (Helm chart).
nix run .#e2e -- --mode kubernetes --scenario single-s3-postgres
```

`task test:e2e` forwards `{{.CLI_ARGS}}` to `nix run .#e2e`; the two entrypoints
are equivalent. `--all` and `--scenario` are mutually exclusive. A multi-scenario
run reports each scenario as PASS/FAIL/SKIP, prints an aggregate summary, and
exits non-zero if **any** scenario FAILED (a SKIP alone never fails the run).

## Modes

| Mode | Substrate | Backends |
| --- | --- | --- |
| `local` | `dev-scripts/run.py` (fixed dev ports) | `nix run .#deps` (Garage/S3, PostgreSQL, MariaDB, Redis) — started and torn down automatically |
| `kubernetes` | Kind cluster + Helm chart | in-cluster Garage/PostgreSQL/MariaDB/Redis |

A scenario declares which modes it supports. Requesting a scenario in a mode it
does not support reports **SKIP** (never PASS). Local mode requires the dev
shell toolchain (`go`, `dbmate`, `direnv`, `watchexec`) that `run.py` drives, so
run it via `task test:e2e` or inside `nix develop`.

## Scenario catalog

Scenarios live in [`config.nix`](./config.nix) — the single source of truth for
both modes. The harness materializes it with
`nix eval --json --file config.nix permutations`. Each entry declares its
dimensions (`storage`, `database`, `replicas`, CDC, staging) and, optionally,
harness-only keys:

- `phase` — `serve` (default), `cdc-lifecycle`, or `staging-contention`.
- `modes` — subset of `local` / `kubernetes` (defaults to both where the
  topology is expressible locally; external-secret / PDB / anti-affinity /
  `migration.mode = "job"` permutations default to `kubernetes` only).

To **add a scenario**, add a permutation entry to `config.nix`. The Kubernetes
`generateValues` path ignores the harness-only keys, so they are inert there.

## Phases

- **`serve`** — seed a NAR through ncps, fetch it from every replica, assert each
  served NAR decompresses byte-identical to the canonical `nix-store --dump`.
- **`cdc-lifecycle`** — drive `non-CDC -> CDC (eager+lazy) -> drain -> non-CDC`,
  asserting serving + DB invariants at each phase (chunking, predictive
  `Compression: none`, `migrate-chunks-to-nar` drain, `initCDCDrainMode`
  auto-exit, fsck repair-not-delete).
- **`staging-contention`** — race N concurrent clients on one large uncached NAR
  across >=2 redis-locker replicas; assert in-flight staging activates (a no-op
  run is a FAILURE) and every reader is byte-identical, across the download
  (CDC off) and chunking (CDC on) windows.

In `kubernetes` mode the plain storage×DB / external-secret / HA permutations
reuse the in-cluster `NCPSTester` validation (serve + CDC-lifecycle topology
checks). The `cdc-lifecycle` **phase-driver** scenario instead runs the *same*
phase driver through a `KubernetesDeployment` adapter, so it is no longer
`local`-only:

- The adapter reaches each replica via a per-pod `kubectl port-forward` and
  writes run.py's `state.json` shape so `seed_cache` builds through the cluster
  ncps.
- CDC is toggled with `helm upgrade --set config.cdc.enabled=… --set config.cdc.lazyChunkingEnabled=…` + `kubectl rollout restart`.
- DB invariants are read in-cluster: postgres/mysql via a port-forward, and
  **sqlite via a `kubectl debug` sidecar** that shares the ncps container's PID
  namespace and reads the live DB at `/proc/1/root` (the ncps image is
  shell-less, so the file can't be read from the ncps container itself). During
  the drain window (ncps scaled to 0) sqlite is read from a transient pod that
  mounts the released storage PVC, and `migrate-chunks-to-nar` runs in a one-shot
  pod cloned from the resolved pod spec.

`staging-contention` stays **`local`-only** (it SKIPs under `--mode kubernetes`):
the adapter supports every seam it needs, but in-flight staging *activation* is a
single-shot timing event that `kubectl port-forward` latency jitter
de-synchronizes, so activation cannot be reliably forced on Kind. The adapter is
ready to lift it later if the race is made deterministic.

## CI

The harness **scenarios** are **manual / opt-in** and are intentionally **not**
part of `nix flake check` (Kind and network-NAR scenarios far exceed the per-PR
budget). A scenario may only be promoted into `nix flake check` if it is proven
to run in under 3 minutes.

The fast, offline **unit tests** for the harness CLI/runner logic
([`tests/`](./tests)) *are* in `nix flake check` as `e2e-harness-unit` (and
`task test:e2e:unit`); they never touch the network or a cluster.

Automated scenario coverage runs on a **nightly** schedule, not on pull requests
— [`.github/workflows/e2e-nightly.yml`](../../.github/workflows/e2e-nightly.yml):

- Triggers on `schedule` (04:00 UTC) and manual `workflow_dispatch`. Never on PRs.
- Runs the full catalog as a matrix over both modes (`--mode local --all` and
  `--mode kubernetes --all`, Kind on the ubuntu runner), `fail-fast: false` so
  one mode failing does not cancel the other.
- **Commit dedup:** a `gate` job records the last successfully-tested `main` SHA
  in an `actions/cache` key (`e2e-nightly-tested-<sha>`) and skips the run when
  `main` has not advanced, so the same commit is never tested two nights running.
  The SHA is recorded only after a fully successful run, so a failure retries on
  the next schedule. `workflow_dispatch` bypasses the gate and forces a run.

## Layout

```text
nix/e2e-tests/
  flake-module.nix       packages.e2e + apps.e2e (writeShellApplication)
  config.nix             scenario catalog (shared by both modes)
  src/
    cli.py               argument parsing (--mode / --scenario / --all / --list)
    catalog.py           load + normalize config.nix
    runner.py            select adapter, manage deps, run phase(s), report
  tests/                 fast offline unit tests (checks.e2e-harness-unit)
    deployment.py        the mode-adapter Protocol
    local.py             LocalDeployment (run.py)
    kubernetes_deployment.py  KubernetesDeployment (phase drivers on Kind)
    kubernetes_mode.py   Kubernetes mode for plain permutations (NCPSTester)
    k8s_tests.py         Kind/Helm backend (cluster, image, install)
    k8s_tests_tester.py  in-cluster NCPSTester validation
    deps.py              fixed-port `nix run .#deps` lifecycle
    client.py            HTTP + NAR fetch / decompress / byte-compare
    db.py                per-dialect DB access for invariant assertions
    phases/              serve, cdc_lifecycle, staging_contention
```
