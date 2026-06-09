## Context

The unified e2e harness phase drivers (`serve`, `cdc-lifecycle`,
`staging-contention`) depend only on the `Deployment` protocol
(`nix/e2e-tests/src/deployment.py`): `provision`, `replica_urls`, `client`,
`restart(cdc, lazy)`, `stop`, `start(cdc, lazy)`, `run_subcommand`, `db`,
`logs`, `teardown`. Only `LocalDeployment` (`local.py`, driving
`dev-scripts/run.py`) implements it.

`kubernetes` mode (`runner._run_kubernetes` → `kubernetes_mode.py`) does **not**
go through the phase drivers at all. It calls `K8sTestsCLI` (Kind cluster, image
build/load, `config.nix`→Helm values, install) and then `NCPSTester`
(`k8s_tests_tester.py`), a separate validation body. `NCPSTester` can serve-check
and run an in-cluster CDC-lifecycle *topology* check, but it:

- only **disables** CDC (no eager/lazy enable toggle the lifecycle driver needs);
- port-forwards the **Service** only (`_test_http_endpoints`), no per-pod
  addressing the staging race needs;
- has no `db()` the drivers can call — its sqlite path uses
  `kubectl debug --image=nouchka/sqlite3:latest` running **as root**, which fails
  with `CreateContainerConfigError` against the chart's `runAsNonRoot`
  securityContext (the known harness blocker), and pg/mysql go through ad-hoc
  port-forwards.

Because no `KubernetesDeployment` exists, `cdc-lifecycle` and
`staging-contention` are pinned `modes = ["local"]` in `config.nix`. This change
builds that adapter so the **same** drivers run on Kind.

## Goals / Non-Goals

**Goals:**
- A `KubernetesDeployment(Deployment)` implementing every protocol seam over Kind
  + Helm, so a phase driver runs **unchanged** under `--mode kubernetes`.
- Route `runner._run_kubernetes` through the phase drivers via the adapter
  (replacing the `NCPSTester` bypass for phase-driver scenarios), preserving the
  existing serve/topology assertions.
- Lift the `cdc-lifecycle` pin so it runs on Kind (verified live, 41/41 checks).
- A fast, offline unit test net for the adapter using a faked `K8sTestsCLI` +
  kubernetes client (no real cluster), runnable in `e2e-harness-unit`.

**Outcome on the second pin (`staging-contention`):** the adapter fully supports
it (per-pod addressing, `read_state` with `inflight_staging`, `clean_restart`,
byte-correct cross-pod serving — all proven live), but in-flight staging
*activation* is a single-shot timing event and `kubectl port-forward` latency
jitter de-synchronizes the thundering-herd race so the lock-holder caches the NAR
before cross-pod waiters contend. Activation could not be reliably forced on
Kind, so this pin **stays `local`-only** with that documented reason (decision
D6) rather than shipping a flaky nightly assertion.

**Non-Goals:**
- New scenarios or changed phase assertions — the drivers are reused verbatim.
- Promoting any Kind scenario into `nix flake check` — they stay nightly-only.
- Replacing `NCPSTester`'s multi-replica *topology* assertions; those remain the
  body the kubernetes `cdc-lifecycle` permutation invokes for cross-replica
  checks (the adapter supplies the substrate; topology checks layer on top).
- Production code, schema, or migration changes.

## Decisions

### D1 — One adapter, drivers unchanged
Add `nix/e2e-tests/src/kubernetes_deployment.py` defining
`KubernetesDeployment(scenario)` that satisfies the `Deployment` protocol by
delegating to a `K8sTestsCLI` instance plus `kubectl`. `runner._run_kubernetes`
constructs it, calls `get_phase(scenario.phase)`, and runs
`phase(deployment, scenario)` exactly like `_run_local` — same try/finally
teardown. The serve/topology validation that today lives in `NCPSTester`
continues to run for the multi-replica `cdc-lifecycle` permutation, invoked from
within the kubernetes `cdc-lifecycle` path.

### D2 — Seam mapping
- `provision()` → `cmd_cluster_create` + `cmd_generate(push=True)` +
  `cmd_install(name=scenario.name)`; wait-ready via existing `_wait_for_pods`.
- `replica_urls()` → one `kubectl port-forward pod/<name> <local>:<8501>` per
  ncps pod (list pods by the release label selector), returning
  `http://127.0.0.1:<local>` per replica. Forwards are owned by the adapter and
  closed in `teardown()`. This is the per-pod addressing staging needs.
- `client(i)` / `logs(i)` → bind to replica *i*'s forwarded URL / `kubectl logs`
  of pod *i*.
- `restart(cdc,lazy)` / `start(cdc,lazy)` / `stop()` → patch the ncps CDC serve
  flags (Helm value / ConfigMap or container args) then `kubectl rollout restart`
  + wait-ready; `stop()` scales the Deployment to 0 replicas. This supplies the
  **enable** toggle `NCPSTester` lacks.
- `run_subcommand(subcmd)` → `kubectl exec` into an ncps pod running
  `ncps <subcmd>` with the scenario's db+storage flags (drain /
  `migrate-chunks-to-nar`).
- `db()` → returns a `DBAccess` whose connection is reached **in-cluster**: for
  postgres/mysql, an adapter-owned port-forward to the data-namespace service
  (reuse `get_cluster_creds`); for sqlite, see D3.

### D3 — sqlite `db()` via a `kubectl debug` sidecar reading `/proc/1/root`
The single-instance `cdc-lifecycle` is sqlite-in-a-pod-PVC. Discovery confirmed
the **ncps production image is shell-less** (`docker.nix` `disallowedRequisites`
on bash/coreutils/busybox/dash/zsh), so the DB file cannot be read from the ncps
container itself (`kubectl exec … cat/sh` has no binary to run). Instead the
adapter attaches a `kubectl debug` **ephemeral container** that shares the ncps
container's PID namespace (`--target ncps`), so the ncps rootfs is reachable at
`/proc/1/root`. The known `CreateContainerConfigError` blocker (a root
`nouchka/sqlite3` image vs the pod's `runAsNonRoot`) is fixed by passing
`--custom` with `securityContext.runAsUser` matched to the ncps container's
effective uid (read from the pod spec; default 1000). Each query copies the live
`ncps.db` (+ `-wal`/`-shm`) from `/proc/1/root/storage/db/` into the sidecar's
writable `/tmp` so WAL writes are visible, then runs `sqlite3`. The sidecar is
created once and re-`exec`'d per query to amortize startup. Postgres/mysql
scenarios keep the simpler adapter-owned port-forward + `DBAccess` path.

### D4 — Offline adapter unit tests
Inject the `K8sTestsCLI` and a `kubectl` runner as constructor seams so tests
pass fakes: assert `provision()` issues create→generate→install in order,
`replica_urls()` opens one forward per pod and `teardown()` closes them all,
`restart(cdc=True)` patches the enable flag + rolls out, `run_subcommand` shells
the right `kubectl exec`, and `db()` selects the correct per-dialect path. These
run in `e2e-harness-unit` (no cluster, `-m "not catalog"`).

### D5 — Drop the `cdc-lifecycle` pin last
Only after the adapter + driver routing are green do we remove
`modes = ["local"]` from `cdc-lifecycle` in `config.nix`, so
`--mode kubernetes --all` runs it instead of SKIPping.

### D6 — Keep `staging-contention` `local`-only (live-Kind finding)
Live Kind runs proved the adapter and the scenario's serving are correct
(8 readers across 2 pods, all HTTP 200, byte-identical to the canonical NAR;
lock contention observed), but the staging-**activation** log never fired across
repeated runs. Root cause: activation is a single-shot race (a cross-pod waiter
must reach ncps while the holder is mid-download, before the NAR is cached), and
the adapter reaches replicas through `kubectl port-forward`, whose per-request
latency jitter smears the driver's synchronized client herd — the holder
downloads and caches `gcc-unwrapped` before the waiters contend. On localhost the
herd stays tight, so the scenario remains reliable in `local` mode. Making it
deterministic on Kind would require gating the contending herd on observing the
holder's first committed staging piece in the logs — a change to the shared phase
driver with regression risk to the hard-won local test — which is out of scope
here. The pin therefore stays, with the reason recorded in `config.nix`.

## Risks / Trade-offs

- **Kind runtime / NAR pulls** (`staging-contention` pulls `gcc-unwrapped`,
  hundreds of MiB per window) → stays nightly-only; never in `nix flake check`.
  Real end-to-end verification happens on Kind locally and on the nightly job.
- **Per-pod port-forward flakiness** → bounded readiness waits + retries,
  mirroring `NCPSTester._test_http_endpoints`; forwards owned and torn down by the
  adapter to avoid leaks.
- **sqlite-in-pod read correctness (WAL)** → checkpoint or read live with `-wal`;
  covered by D3 and an explicit lifecycle assertion.
- **Overlap with `NCPSTester`** → the adapter reuses `K8sTestsCLI` helpers and
  keeps `NCPSTester`'s topology checks as the cross-replica body, rather than
  forking a second assertions path.
- **CDC enable toggle plumbing** (Helm value vs ConfigMap vs container args) →
  pick whichever the chart already exposes for the CDC serve flags to minimize
  new chart surface; if none exists, prefer a values-patch + rollout over editing
  rendered manifests.
- **Verification cost** → the adapter logic is unit-tested offline (D4); the
  full Kind run is exercised manually/nightly, not on every push, so a red Kind
  leg never blocks a PR.

## Open Questions — resolved during implementation

- **CDC toggle:** the chart exposes `config.cdc.enabled` and
  `config.cdc.lazyChunkingEnabled` as Helm values (rendered into a ConfigMap), so
  the adapter toggles CDC with `helm upgrade --set config.cdc.enabled=… --set
  config.cdc.lazyChunkingEnabled=…` followed by `kubectl rollout restart`. No
  raw manifest editing.
- **sqlite invariants:** there is no `ncps` subcommand that answers the chunk-row
  invariants, and the production image is shell-less, so the in-pod sqlite reader
  is required — implemented as the `kubectl debug` + `/proc/1/root` sidecar in D3.
- **Remaining (live-Kind only):** exact effective uid of the ncps container under
  the generated test values (the sidecar reads it from the pod spec, defaulting
  to 1000), and `clean_restart` cache-wipe completeness for the s3+pg staging
  scenario — both validated by the manual Kind run (task 9.3), not unit tests.
