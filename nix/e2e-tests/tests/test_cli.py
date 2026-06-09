"""Unit tests for the harness CLI argument parsing and selection routing."""

from __future__ import annotations

import pytest

import cli


def test_scenario_is_repeatable():
    args = cli.build_parser().parse_args(
        ["--mode", "local", "--scenario", "a", "--scenario", "b"]
    )
    assert args.scenario == ["a", "b"]


def test_scenario_comma_split():
    args = cli.build_parser().parse_args(["--mode", "local", "--scenario", "a,b,c"])
    assert cli._split_scenarios(args.scenario) == ["a", "b", "c"]


def test_split_scenarios_flattens_and_trims():
    assert cli._split_scenarios(["a, b", "c"]) == ["a", "b", "c"]
    assert cli._split_scenarios(None) == []


def test_all_flag_present():
    args = cli.build_parser().parse_args(["--mode", "local", "--all"])
    assert args.all is True


def test_all_and_scenario_are_mutually_exclusive(capsys):
    with pytest.raises(SystemExit):
        cli.main(["--mode", "local", "--all", "--scenario", "a"])


def test_run_requires_a_selection(capsys):
    # --mode given but neither --scenario nor --all.
    with pytest.raises(SystemExit):
        cli.main(["--mode", "local"])


def test_all_routes_to_run_scenarios_with_none(monkeypatch):
    captured = {}

    def fake_run_scenarios(*, mode, scenario_names, verbose=False):
        captured["mode"] = mode
        captured["names"] = scenario_names
        return 0

    import runner

    monkeypatch.setattr(runner, "run_scenarios", fake_run_scenarios)
    rc = cli.main(["--mode", "kubernetes", "--all"])
    assert rc == 0
    assert captured["mode"] == "kubernetes"
    assert captured["names"] is None  # None == "every scenario"


def test_explicit_scenarios_route_as_list(monkeypatch):
    captured = {}

    def fake_run_scenarios(*, mode, scenario_names, verbose=False):
        captured["names"] = scenario_names
        return 0

    import runner

    monkeypatch.setattr(runner, "run_scenarios", fake_run_scenarios)
    cli.main(["--mode", "local", "--scenario", "a,b", "--scenario", "c"])
    assert captured["names"] == ["a", "b", "c"]


def test_single_scenario_still_works(monkeypatch):
    captured = {}

    def fake_run_scenarios(*, mode, scenario_names, verbose=False):
        captured["names"] = scenario_names
        return 0

    import runner

    monkeypatch.setattr(runner, "run_scenarios", fake_run_scenarios)
    cli.main(["--mode", "local", "--scenario", "cdc-lifecycle"])
    assert captured["names"] == ["cdc-lifecycle"]
