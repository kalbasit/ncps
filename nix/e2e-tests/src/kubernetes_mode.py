"""Kubernetes mode: drive a scenario on a Kind cluster via the Helm chart.

Reuses the proven ``K8sTestsCLI`` machinery (Kind cluster lifecycle, ncps image
build/load, ``config.nix`` -> Helm values generation, install) and the
``NCPSTester`` validation body — including its CDC-lifecycle topology checks —
rather than reimplementing port-forward + assertions. This is the consolidated
home of the former ``k8s-tests`` CLI: it is no longer a user-facing command, but
the kubernetes backend of the unified harness.
"""

from __future__ import annotations

from harness_config import G, R, log, section

# Kind exposes the in-cluster registry on this host port (see cmd_cluster_create).
_REGISTRY = "localhost:30000"
_REPOSITORY = "ncps"


def run_kubernetes_scenario(scenario, *, verbose: bool = False, reuse_image: bool = False) -> int:
    """Provision the scenario on Kind, run NCPSTester validation, clean up.

    Returns 0 on PASS, 1 on FAIL. The Kind cluster is created idempotently and
    left running (cluster teardown is a separate, explicit operation); only the
    scenario's Helm release is cleaned up.
    """
    from k8s_tests import K8sTestsCLI

    cli = K8sTestsCLI(verbose=verbose)
    name = scenario.name
    rc = 0
    try:
        section(f"KUBERNETES — {name}: provisioning Kind + Helm")
        cli.cmd_cluster_create()
        # Build+load the ncps image and generate per-scenario Helm values.
        # reuse_image lets repeated runs skip the (slow) image rebuild.
        cli.cmd_generate(
            push=True,
            last=reuse_image,
            tag=None,
            registry=_REGISTRY,
            repository=_REPOSITORY,
        )
        cli.cmd_install(name=name)
        # NCPSTester validates the deployment (serve + topology + lifecycle);
        # it calls sys.exit(1) on failure.
        cli.cmd_test(name=name)
        section(f"PASS {name} [kubernetes]")
        log(f"PASS {name} [kubernetes]", G)
    except SystemExit as e:
        if e.code:
            log(f"FAIL {name} [kubernetes]: validation failed", R)
            rc = 1
    except Exception as e:  # noqa: BLE001 — surface any error as a run failure
        log(f"ERROR {name} [kubernetes]: {e}", R)
        rc = 1
    finally:
        try:
            cli.cmd_cleanup(name)
        except Exception as e:  # noqa: BLE001 — cleanup is best-effort
            log(f"kubernetes: cleanup error (ignored): {e}", R)
    return rc
