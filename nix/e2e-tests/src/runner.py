"""Scenario runner: select adapter, manage deps, run the phase, report, teardown.

Reports PASS / FAIL / SKIP uniformly and exits non-zero on any failure. SKIP is
returned (exit 0) when the scenario's topology is unsupported in the chosen mode
— never reported as PASS. Dependencies the harness starts are always torn down.
"""

from __future__ import annotations

from catalog import find_scenario
from harness_config import AssertionFailure, G, R, Y, log, section
from phases import get_phase


def run_scenario(*, mode: str, scenario_name: str, verbose: bool = False) -> int:
    try:
        scenario = find_scenario(scenario_name)
    except KeyError as e:
        log(str(e), R)
        return 2

    # SKIP (not FAIL, not PASS) when the topology can't be expressed in this mode.
    if not scenario.supports(mode):
        log(
            f"SKIP {scenario.name} [{mode}]: topology unsupported in this mode "
            f"(supported modes: {', '.join(scenario.modes)})",
            Y,
        )
        return 0

    if mode == "local":
        return _run_local(scenario, verbose)
    if mode == "kubernetes":
        return _run_kubernetes(scenario, verbose)
    log(f"unknown mode: {mode}", R)
    return 2


def _run_local(scenario, verbose: bool) -> int:
    from deps import Deps
    from local import LocalDeployment

    needs_redis = scenario.staging or scenario.replicas > 1
    deps = Deps(needs_redis=needs_redis)
    deployment = LocalDeployment(scenario)
    phase = get_phase(scenario.phase)

    rc = 0
    try:
        deps.ensure_up()
        deployment.provision()
        phase(deployment, scenario)
        section(f"PASS {scenario.name} [local]")
        log(f"PASS {scenario.name} [local]", G)
    except AssertionFailure as e:
        log(f"FAIL {scenario.name} [local]: {e}", R)
        rc = 1
    except Exception as e:  # noqa: BLE001 — surface any error as a run failure
        log(f"ERROR {scenario.name} [local]: {e}", R)
        rc = 1
    finally:
        deployment.teardown()
        deps.teardown()
    return rc


def _run_kubernetes(scenario, verbose: bool) -> int:
    # Kubernetes mode delegates to the reused K8sTestsCLI + NCPSTester backend,
    # which already validates serve and CDC-lifecycle topology in-cluster.
    from kubernetes_mode import run_kubernetes_scenario

    return run_kubernetes_scenario(scenario, verbose=verbose)
