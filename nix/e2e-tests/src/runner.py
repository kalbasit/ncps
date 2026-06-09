"""Scenario runner: select adapter, manage deps, run the phase, report, teardown.

Reports PASS / FAIL / SKIP uniformly and exits non-zero on any failure. SKIP is
reported (and does not fail the run) when a scenario's topology is unsupported in
the chosen mode — never reported as PASS. A single invocation may run one
scenario, an explicit set, or every catalog scenario (`run_scenarios`).
Dependencies the harness starts are always torn down.
"""

from __future__ import annotations

from catalog import find_scenario, load_catalog
from harness_config import AssertionFailure, G, R, Y, log, section
from phases import get_phase


def _execute(scenario, mode: str, verbose: bool) -> str:
    """Run one scenario in one mode, returning 'PASS' | 'FAIL' | 'SKIP'."""
    # SKIP (not FAIL, not PASS) when the topology can't be expressed in this mode.
    if not scenario.supports(mode):
        log(
            f"SKIP {scenario.name} [{mode}]: topology unsupported in this mode "
            f"(supported modes: {', '.join(scenario.modes)})",
            Y,
        )
        return "SKIP"

    if mode == "local":
        rc = _run_local(scenario, verbose)
    elif mode == "kubernetes":
        rc = _run_kubernetes(scenario, verbose)
    else:
        log(f"unknown mode: {mode}", R)
        return "FAIL"
    return "PASS" if rc == 0 else "FAIL"


def run_scenario(*, mode: str, scenario_name: str, verbose: bool = False) -> int:
    try:
        scenario = find_scenario(scenario_name)
    except KeyError as e:
        log(str(e), R)
        return 2

    status = _execute(scenario, mode, verbose)
    # SKIP and PASS both exit 0; only FAIL is non-zero.
    return 1 if status == "FAIL" else 0


def run_scenarios(*, mode: str, scenario_names, verbose: bool = False) -> int:
    """Run a set of scenarios in one invocation and aggregate the result.

    ``scenario_names`` is ``None`` to run every catalog scenario, or an explicit
    list of names. Prints a per-scenario summary and returns non-zero iff any
    selected scenario FAILED (a SKIP never fails the run).
    """
    catalog = load_catalog()

    if scenario_names is None:
        selected = list(catalog)
    else:
        selected = []
        for name in scenario_names:
            try:
                selected.append(find_scenario(name, catalog))
            except KeyError as e:
                log(str(e), R)
                return 2

    results = []
    for scenario in selected:
        status = _execute(scenario, mode, verbose)
        results.append((scenario.name, status))

    _print_summary(mode, results)
    return 1 if any(status == "FAIL" for _, status in results) else 0


def _print_summary(mode: str, results) -> None:
    section(f"SUMMARY [{mode}] — {len(results)} scenario(s)")
    color = {"PASS": G, "FAIL": R, "SKIP": Y}
    for name, status in results:
        log(f"  {status:4} {name}", color.get(status, R))
    passed = sum(1 for _, s in results if s == "PASS")
    failed = sum(1 for _, s in results if s == "FAIL")
    skipped = sum(1 for _, s in results if s == "SKIP")
    log(
        f"  totals: {passed} passed, {failed} failed, {skipped} skipped",
        R if failed else G,
    )


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


# Catalog scenarios that run their full phase driver through the
# KubernetesDeployment adapter (so the *same* driver body executes on Kind),
# rather than the NCPSTester serve/topology validation. Gated by name, NOT by
# phase: `ha-s3-postgres-cdc-lifecycle` also has phase "cdc-lifecycle" but is a
# multi-replica permutation whose cross-replica topology checks belong to
# NCPSTester, so it must NOT be routed here. `staging-contention` is listed for
# completeness — it is pinned `local`-only, so it SKIPs before reaching this.
_ADAPTER_SCENARIOS = ("cdc-lifecycle", "staging-contention")


def _run_kubernetes(scenario, verbose: bool) -> int:
    # The single-instance / external-secret / HA permutations keep the proven
    # K8sTestsCLI + NCPSTester backend (serve + CDC-lifecycle topology). The
    # explicitly-lifted phase-driver scenarios run the shared driver through the
    # adapter instead.
    if scenario.name not in _ADAPTER_SCENARIOS:
        from kubernetes_mode import run_kubernetes_scenario

        return run_kubernetes_scenario(scenario, verbose=verbose)

    from kubernetes_deployment import KubernetesDeployment

    deployment = KubernetesDeployment(scenario)
    phase = get_phase(scenario.phase)
    rc = 0
    try:
        deployment.provision()
        phase(deployment, scenario)
        section(f"PASS {scenario.name} [kubernetes]")
        log(f"PASS {scenario.name} [kubernetes]", G)
    except AssertionFailure as e:
        log(f"FAIL {scenario.name} [kubernetes]: {e}", R)
        rc = 1
    except Exception as e:  # noqa: BLE001 — surface any error as a run failure
        log(f"ERROR {scenario.name} [kubernetes]: {e}", R)
        rc = 1
    finally:
        deployment.teardown()
    return rc
