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

# Run a scenario on a Kind cluster (Helm chart).
nix run .#e2e -- --mode kubernetes --scenario single-s3-postgres
```

`task test:e2e` forwards `{{.CLI_ARGS}}` to `nix run .#e2e`; the two entrypoints
are equivalent.

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

In `kubernetes` mode the harness reuses the in-cluster `NCPSTester` validation
(serve + CDC-lifecycle topology checks).

## CI

This harness is **manual / opt-in** and is intentionally **not** part of
`nix flake check` (Kind and network-NAR scenarios far exceed the per-PR budget).
A scenario may only be promoted into `nix flake check` if it is proven to run in
under 3 minutes. Automated coverage, if wanted, belongs on a scheduled (nightly)
workflow, not on pull requests.

## Layout

```text
nix/e2e-tests/
  flake-module.nix       packages.e2e + apps.e2e (writeShellApplication)
  config.nix             scenario catalog (shared by both modes)
  src/
    cli.py               argument parsing (--mode / --scenario / --list)
    catalog.py           load + normalize config.nix
    runner.py            select adapter, manage deps, run phase, report
    deployment.py        the mode-adapter Protocol
    local.py             LocalDeployment (run.py)
    kubernetes_mode.py   Kubernetes mode (delegates to the backend below)
    k8s_tests.py         Kind/Helm backend (cluster, image, install)
    k8s_tests_tester.py  in-cluster NCPSTester validation
    deps.py              fixed-port `nix run .#deps` lifecycle
    client.py            HTTP + NAR fetch / decompress / byte-compare
    db.py                per-dialect DB access for invariant assertions
    phases/              serve, cdc_lifecycle, staging_contention
```
