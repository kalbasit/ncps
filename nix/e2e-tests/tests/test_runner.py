"""Unit tests for multi-scenario aggregation in the runner."""

from __future__ import annotations

from dataclasses import dataclass
from typing import List

import runner


@dataclass
class _FakeScenario:
    name: str
    modes: List[str]
    staging: bool = False
    replicas: int = 1

    def supports(self, mode: str) -> bool:
        return mode in self.modes


def _fake_catalog():
    return [
        _FakeScenario("alpha", ["local", "kubernetes"]),
        _FakeScenario("bravo", ["local", "kubernetes"]),
        _FakeScenario("charlie", ["kubernetes"]),  # local-unsupported
    ]


def _patch(monkeypatch, statuses):
    """Patch catalog + per-scenario execution; statuses maps name -> result."""
    monkeypatch.setattr(runner, "load_catalog", _fake_catalog)
    # Neutralize the shared local-deps lifecycle so these aggregation tests never
    # touch real backends.
    monkeypatch.setattr(runner, "_make_local_deps", lambda scenarios: None)

    def fake_execute(scenario, mode, verbose, shared_deps=None):
        return statuses[scenario.name]

    monkeypatch.setattr(runner, "_execute", fake_execute)


def test_run_kubernetes_routes_phase_drivers_through_adapter(monkeypatch):
    """cdc-lifecycle / staging-contention run via KubernetesDeployment, not NCPSTester."""
    calls = {"adapter": 0, "ncps_tester": 0, "torn_down": False}

    class _FakeDeployment:
        def __init__(self, scenario):
            calls["adapter"] += 1

        def provision(self):
            pass

        def teardown(self):
            calls["torn_down"] = True

    monkeypatch.setattr(
        runner, "get_phase", lambda name: (lambda dep, sc: None)
    )
    import kubernetes_deployment
    import kubernetes_mode

    monkeypatch.setattr(
        kubernetes_deployment, "KubernetesDeployment", _FakeDeployment
    )
    # Wire the NCPSTester path so the assertion below is meaningful (would
    # increment if the scenario wrongly fell through to NCPSTester).
    monkeypatch.setattr(
        kubernetes_mode,
        "run_kubernetes_scenario",
        lambda scenario, verbose=False: calls.__setitem__("ncps_tester", calls["ncps_tester"] + 1) or 0,
    )

    @dataclass
    class _PhaseScenario:
        name: str
        phase: str
        modes: List[str]

        def supports(self, mode):
            return mode in self.modes

    rc = runner._run_kubernetes(
        _PhaseScenario("staging-contention", "staging-contention", ["kubernetes"]),
        verbose=False,
    )
    assert rc == 0
    assert calls["adapter"] == 1, "adapter constructed for phase-driver scenario"
    assert calls["torn_down"], "teardown always called"
    assert calls["ncps_tester"] == 0


def test_run_kubernetes_keeps_ncps_tester_for_plain_permutations(monkeypatch):
    """serve permutations still use the K8sTestsCLI + NCPSTester backend."""
    seen = {"ncps_tester": 0}
    import kubernetes_mode

    monkeypatch.setattr(
        kubernetes_mode,
        "run_kubernetes_scenario",
        lambda scenario, verbose=False: seen.__setitem__("ncps_tester", 1) or 0,
    )

    @dataclass
    class _ServeScenario:
        name: str
        phase: str
        modes: List[str]

        def supports(self, mode):
            return mode in self.modes

    rc = runner._run_kubernetes(
        _ServeScenario("single-s3-postgres", "serve", ["kubernetes"]), verbose=False
    )
    assert rc == 0
    assert seen["ncps_tester"] == 1, "plain permutation routed to NCPSTester"


def test_run_kubernetes_ha_cdc_lifecycle_stays_on_ncps_tester(monkeypatch):
    """A multi-replica permutation that shares the cdc-lifecycle PHASE but is not
    an explicitly-lifted scenario keeps NCPSTester's topology checks (gated by
    name, not phase)."""
    seen = {"ncps_tester": 0}
    import kubernetes_mode

    monkeypatch.setattr(
        kubernetes_mode,
        "run_kubernetes_scenario",
        lambda scenario, verbose=False: seen.__setitem__("ncps_tester", 1) or 0,
    )

    @dataclass
    class _HaScenario:
        name: str
        phase: str
        modes: List[str]

        def supports(self, mode):
            return mode in self.modes

    rc = runner._run_kubernetes(
        _HaScenario("ha-s3-postgres-cdc-lifecycle", "cdc-lifecycle", ["kubernetes"]),
        verbose=False,
    )
    assert rc == 0
    assert seen["ncps_tester"] == 1, "HA cdc-lifecycle permutation routed to NCPSTester, not the adapter"


def test_all_selects_every_scenario(monkeypatch):
    seen = []
    monkeypatch.setattr(runner, "load_catalog", _fake_catalog)

    def fake_execute(scenario, mode, verbose, shared_deps=None):
        seen.append(scenario.name)
        return "PASS"

    monkeypatch.setattr(runner, "_execute", fake_execute)
    rc = runner.run_scenarios(mode="kubernetes", scenario_names=None)
    assert rc == 0
    assert seen == ["alpha", "bravo", "charlie"]


def test_explicit_names_run_only_those(monkeypatch):
    seen = []
    monkeypatch.setattr(runner, "load_catalog", _fake_catalog)
    # Neutralize the shared local-deps lifecycle for this local-mode aggregation.
    monkeypatch.setattr(runner, "_make_local_deps", lambda scenarios: None)

    def fake_execute(scenario, mode, verbose, shared_deps=None):
        seen.append(scenario.name)
        return "PASS"

    monkeypatch.setattr(runner, "_execute", fake_execute)
    rc = runner.run_scenarios(mode="local", scenario_names=["alpha", "bravo"])
    assert rc == 0
    assert seen == ["alpha", "bravo"]


def test_local_multi_scenario_starts_deps_once(monkeypatch):
    """A local --all run starts the managed backends once before the loop and
    tears them down once after — not per scenario — and includes Redis when any
    selected scenario needs it."""
    counts = {"created": 0, "ensure_up": 0, "teardown": 0, "needs_redis": None}

    class _SpyDeps:
        def __init__(self, *, needs_redis=False):
            counts["created"] += 1
            counts["needs_redis"] = needs_redis

        def ensure_up(self, timeout=300):
            counts["ensure_up"] += 1

        def teardown(self):
            counts["teardown"] += 1

    import deps as deps_mod

    monkeypatch.setattr(deps_mod, "Deps", _SpyDeps)

    def _catalog():
        return [
            _FakeScenario("alpha", ["local", "kubernetes"]),
            _FakeScenario("bravo", ["local", "kubernetes"], replicas=2),  # needs redis
            _FakeScenario("charlie", ["kubernetes"]),  # local-unsupported
        ]

    monkeypatch.setattr(runner, "load_catalog", _catalog)

    seen_shared = []

    def fake_run_local(scenario, verbose, shared_deps=None):
        seen_shared.append(shared_deps)
        return 0

    monkeypatch.setattr(runner, "_run_local", fake_run_local)

    rc = runner.run_scenarios(mode="local", scenario_names=None)
    assert rc == 0
    assert counts["created"] == 1, "deps constructed exactly once"
    assert counts["ensure_up"] == 1, "backends started exactly once"
    assert counts["teardown"] == 1, "backends torn down exactly once"
    assert counts["needs_redis"] is True, "redis included because a scenario needs it"
    # Both local-supported scenarios received the shared deps (so _run_local does
    # not start its own); charlie skipped before reaching _run_local.
    assert len(seen_shared) == 2
    assert all(s is not None for s in seen_shared)


def test_any_failure_makes_exit_nonzero(monkeypatch):
    _patch(monkeypatch, {"alpha": "PASS", "bravo": "FAIL", "charlie": "SKIP"})
    rc = runner.run_scenarios(mode="kubernetes", scenario_names=None)
    assert rc == 1


def test_skip_alone_is_success(monkeypatch):
    _patch(monkeypatch, {"alpha": "PASS", "bravo": "SKIP", "charlie": "SKIP"})
    rc = runner.run_scenarios(mode="kubernetes", scenario_names=None)
    assert rc == 0


def test_unknown_name_fails_fast(monkeypatch):
    monkeypatch.setattr(runner, "load_catalog", _fake_catalog)
    rc = runner.run_scenarios(mode="local", scenario_names=["nope"])
    assert rc == 2


def test_summary_lists_each_result(monkeypatch, capsys):
    _patch(monkeypatch, {"alpha": "PASS", "bravo": "FAIL", "charlie": "SKIP"})
    runner.run_scenarios(mode="kubernetes", scenario_names=None)
    out = capsys.readouterr().out
    assert "alpha" in out and "bravo" in out and "charlie" in out
    assert "PASS" in out and "FAIL" in out and "SKIP" in out
