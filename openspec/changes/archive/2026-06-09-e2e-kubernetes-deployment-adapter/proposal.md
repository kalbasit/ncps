## Why

The unified e2e harness runs the `cdc-lifecycle` and `staging-contention`
scenarios in `local` mode only — they are pinned `modes = ["local"]` in
`config.nix`. The phase drivers bind to a `Deployment` protocol that only the
local (`run.py`) backend implements; kubernetes mode bypasses the drivers
entirely and delegates to a separate `NCPSTester`. As a result the harness's two
most valuable behaviors — the full CDC lifecycle and real multi-replica staging
contention — are never exercised on Kubernetes, the substrate production runs on.

## What Changes

- Add a `KubernetesDeployment` that implements the same `Deployment` protocol the
  phase drivers already use, backed by the existing `K8sTestsCLI` + kubernetes
  client, so `cdc-lifecycle` and `staging-contention` run **unchanged** on Kind.
- Implement the three seams discovery proved are missing on the kubernetes side:
  - **CDC enable/lazy toggle** — `restart()/stop()/start()` flip CDC via a
    ConfigMap/Helm-values change + `kubectl rollout restart` + wait-ready
    (today's tester can only *disable* CDC).
  - **Per-pod addressing** — `replica_urls()` returns a per-pod `kubectl
    port-forward` URL each (today only the Service is forwarded; per-replica
    addressing is needed for any multi-replica phase driver).
  - **`db()` invariant access** — sqlite via `kubectl exec` into the pod's PVC,
    and pg/mysql via port-forward, so the drivers' DB-invariant assertions run
    in-cluster.
- Drop the `modes = ["local"]` pin on `cdc-lifecycle` so the nightly `--mode
  kubernetes --all` leg runs it instead of SKIPping.
- Keep `staging-contention` `local`-only (its pin stays). Live Kind verification
  proved the adapter, per-pod racing and byte-correct serving all work, but
  in-flight staging *activation* is a single-shot timing event that port-forward
  latency jitter de-synchronizes, so activation cannot be reliably forced on
  Kind. The pin now carries that documented reason. The adapter still implements
  every seam the scenario uses, so it can be lifted later if the race is made
  deterministic.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `unified-e2e-harness`: the **CDC lifecycle scenario** and **In-flight staging
  contention scenario** requirements gain kubernetes-mode coverage (currently
  only `local` mode satisfies them); the **Mode-selectable execution** requirement
  is strengthened so a substrate-agnostic scenario genuinely runs unchanged in
  either mode rather than SKIPping under kubernetes.

## Impact

- **Code:** `nix/e2e-tests/src/` (new `kubernetes_deployment.py` adapter; runner
  wiring to select it for the phase-driver path), `nix/e2e-tests/tests/` (adapter
  unit coverage with a faked CLI/client), `nix/e2e-tests/config.nix` (remove two
  pins). No ncps production code, schema, or migration is touched.
- **CI:** the nightly kubernetes matrix leg gains two long Kind scenarios; the
  `staging-contention` pull of `gcc-unwrapped` (several hundred MiB per window)
  dominates runtime. These scenarios stay **nightly-only** and MUST NOT enter
  `nix flake check`.
- **I/O / network / memory:** test-harness only — no change to ncps runtime I/O,
  network latency, or memory. Added cost is CI wall-clock (Kind provisioning,
  port-forwards, large NAR pulls) on the scheduled run, not the per-PR path.
