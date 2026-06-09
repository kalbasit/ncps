## Context

The unified e2e harness (`nix/e2e-tests/`) runs one scenario in one mode per
invocation. Its two backends are asymmetric:

- **local mode** drives ncps through `dev-scripts/run.py` behind a clean
  `Deployment` protocol (`replica_urls`, `client`, `restart`, `run_subcommand`,
  `db`, `logs`). The feature phases (`serve`, `cdc-lifecycle`,
  `staging-contention`) are written against this protocol only.
- **kubernetes mode** (`kubernetes_mode.py`) does *not* use that protocol. It
  delegates to `K8sTestsCLI` + `NCPSTester` (`k8s_tests_tester.py`), a separate
  validation body that port-forwards, asserts serve + DB/storage invariants, and
  runs an in-cluster CDC-lifecycle topology check for HA permutations.

Because the phase drivers bind to `Deployment` and no `KubernetesDeployment`
exists, `cdc-lifecycle` and `staging-contention` are pinned `modes = ["local"]`
in `config.nix`. Nothing runs the catalog on a schedule.

This change adds multi-scenario selection, lifts those pins by giving the phase
drivers a kubernetes substrate, and adds a nightly CI workflow with
commit-dedup.

## Goals / Non-Goals

**Goals:**
- One invocation can run many scenarios (`--all`, repeatable/comma `--scenario`)
  with per-scenario PASS/FAIL/SKIP and an aggregate non-zero-on-any-failure exit.
- A nightly workflow runs the full catalog as a `local`+`kubernetes` matrix and
  skips when `main` is unchanged since the last successful run.

**Non-Goals:**
- Lifting the `cdc-lifecycle` / `staging-contention` local-only pins — **deferred
  to a follow-up change** (see D2; it needs new k8s plumbing out of scope here).
- Intra-process scenario parallelism (the CI matrix parallelizes at job level).
- Promoting any scenario into `nix flake check` (only a fast offline harness unit
  check is added).
- New scenarios or changed phase assertions.

## Decisions

### D1 — Multi-scenario selection in the runner, not per scenario
`--scenario` becomes `action="append"` and each value is comma-split; add
`--all`. `--all` and `--scenario` are mutually exclusive. A new
`run_scenarios(mode, names, …)` resolves the set (for `--all`: every catalog
scenario; unknown names still fail fast), runs each via the existing
single-scenario path, prints a final summary table, and returns `0` only if
none FAILED (SKIP is not a failure). Single `--scenario <name>` is unchanged.
*Alternative rejected:* a shell `for` loop in the Nix wrapper — loses unified
reporting and the shared catalog load.

### D2 — Lift the pins with a real `KubernetesDeployment` adapter — DEFERRED
**Status: carved out into a follow-up change.** The intended approach is a
`KubernetesDeployment(Deployment)` so the **same** phase drivers run on Kind,
with the seams mapped onto `K8sTestsCLI` + the kubernetes client:
- `provision()` → `cmd_cluster_create` + `cmd_generate` + `cmd_install`.
- `replica_urls()` → per-pod `kubectl port-forward` (the tester only forwards the
  Service; staging-contention needs per-replica addressing).
- `logs(i)` → `kubectl logs` of replica `i`.
- `restart()/stop()/start()` → ConfigMap CDC enable/disable toggle + `kubectl
  rollout restart` + wait-ready.
- `run_subcommand()` → `kubectl exec` (drain).
- `db()` → a new per-dialect in-cluster path: **sqlite via `kubectl exec`** and
  pg/mysql via port-forward.

*Why deferred:* implementation discovery showed the seams are **not** all already
present. NCPSTester can only *disable* CDC (no enable/lazy toggle), forwards only
the Service (no per-pod addressing), and has no direct `db()` the phase drivers
can call — the single-instance `cdc-lifecycle` is sqlite-in-a-pod-PVC, so its DB
invariants need a sqlite-via-exec path that does not exist. That is substantial
new plumbing plus long Kind verification cycles (the `staging-contention`
scenario pulls `gcc-unwrapped`, several hundred MiB, per window). Splitting it
out lets the verified multi-scenario + nightly work ship now. When the follow-up
lands it will drop the `modes = ["local"]` pins in `config.nix`; until then the
nightly `--mode kubernetes --all` leg SKIPs those two scenarios.

### D3 — Nightly workflow with cache-keyed commit dedup
New `.github/workflows/e2e-nightly.yml`: `schedule` (cron, nightly) +
`workflow_dispatch`. A `gate` job resolves the current `main` SHA and probes an
`actions/cache` entry keyed by that SHA (`e2e-nightly-tested-<sha>`); on a cache
**hit** it sets `skip=true` and the matrix jobs are skipped. After a fully
successful matrix run, a final job *writes* that cache key, recording the tested
SHA so the next night short-circuits. `workflow_dispatch` can force a run
(bypass the gate).
*Alternatives rejected:* committing a SHA file back to the repo (needs write
perms, noisy history); a git tag/note (same perms); querying `gh run list` for
the last green SHA (works, but cache is simpler and needs no token scope).
*Matrix:* over `mode ∈ {local, kubernetes}`; each leg runs `nix run .#e2e --
--mode <mode> --all`. Kind runs on the `ubuntu` runner.

## Risks / Trade-offs

- **Kind flake/runtime on CI** → matrix isolates the kubernetes leg; a failing
  k8s leg does not mask the local leg; nightly cadence absorbs slowness.
- **`actions/cache` eviction (7-day / size LRU) re-runs an already-tested SHA**
  → acceptable: worst case is one redundant run, never a missed regression.
- **port-forward flakiness for `replica_urls()`** → bounded retries/readiness
  waits, mirroring the tester's existing port-forward handling.
- **Scope creep building the k8s adapter** → reuse `NCPSTester` helpers as the
  implementation of each seam instead of new code paths.
- **`--all` masking individual failures** → explicit per-scenario lines plus a
  summary table; aggregate exit is non-zero if any FAILED.

## Migration Plan

Additive only. CLI change is backward-compatible (single `--scenario` still
works). No production code, schema, or migration touched. Rollback = revert the
harness/workflow commits; no deployed state to unwind.

## Open Questions

- Nightly cron hour (pick a low-traffic UTC slot; default 04:00 UTC).
- Whether to also matrix the kubernetes leg per-scenario if `--all` in one job
  exceeds the runner time budget — deferred until first nightly timings exist.
