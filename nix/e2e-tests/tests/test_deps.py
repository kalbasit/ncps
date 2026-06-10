"""Unit tests for the backing-services readiness lifecycle.

``deps`` imports only stdlib + ``harness_config`` (light), so these run under the
pytest-only ``e2e-harness-unit`` check without ``nix``/network.
"""

from __future__ import annotations

import inspect

import pytest

import deps as deps_mod


def test_ensure_up_timeout_tolerates_cold_ci_boot():
    """A cold all-backend boot on a constrained runner can exceed 120s; the
    default readiness timeout must allow at least 300s."""
    default = inspect.signature(deps_mod.Deps.ensure_up).parameters["timeout"].default
    assert default >= 300


def test_readiness_timeout_dumps_process_compose_diagnostics(monkeypatch):
    """On a readiness timeout the harness must surface which backend is unready
    (process list at minimum), not just an opaque 'not ready' message."""
    commands = []

    class _R:
        returncode = 0
        stdout = "garage-server\npostgres-server\nmariadb-server\n"
        stderr = ""

    def fake_run(cmd, **kwargs):
        commands.append(list(cmd))
        return _R()

    # Ports never become ready -> force the timeout path.
    monkeypatch.setattr(deps_mod, "_all_ports_ready", lambda ports: False)
    monkeypatch.setattr(deps_mod.subprocess, "run", fake_run)

    d = deps_mod.Deps()
    with pytest.raises(RuntimeError):
        d.ensure_up(timeout=0)

    joined = [" ".join(str(p) for p in c) for c in commands]
    assert any("process" in j and "list" in j for j in joined), (
        "diagnostics must include a process-compose process list"
    )
