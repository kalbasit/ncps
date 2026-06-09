"""Unit tests for multi-scenario aggregation in the runner."""

from __future__ import annotations

from dataclasses import dataclass
from typing import List

import runner


@dataclass
class _FakeScenario:
    name: str
    modes: List[str]

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

    def fake_execute(scenario, mode, verbose):
        return statuses[scenario.name]

    monkeypatch.setattr(runner, "_execute", fake_execute)


def test_all_selects_every_scenario(monkeypatch):
    seen = []
    monkeypatch.setattr(runner, "load_catalog", _fake_catalog)

    def fake_execute(scenario, mode, verbose):
        seen.append(scenario.name)
        return "PASS"

    monkeypatch.setattr(runner, "_execute", fake_execute)
    rc = runner.run_scenarios(mode="kubernetes", scenario_names=None)
    assert rc == 0
    assert seen == ["alpha", "bravo", "charlie"]


def test_explicit_names_run_only_those(monkeypatch):
    seen = []
    monkeypatch.setattr(runner, "load_catalog", _fake_catalog)

    def fake_execute(scenario, mode, verbose):
        seen.append(scenario.name)
        return "PASS"

    monkeypatch.setattr(runner, "_execute", fake_execute)
    rc = runner.run_scenarios(mode="local", scenario_names=["alpha", "bravo"])
    assert rc == 0
    assert seen == ["alpha", "bravo"]


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
