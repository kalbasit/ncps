"""Fixed-port backing-services lifecycle (Garage/S3, PG, MariaDB, Redis).

Replaces the former ``test-*-auto.sh`` wrappers: starts ``nix run .#deps`` on a
dedicated process-compose control port, waits for the fixed ports, and — per the
spec — stops only the services this harness started. If the ports are already
reachable, an externally-managed stack is assumed and left untouched.
"""

from __future__ import annotations

import json
import os
import socket
import subprocess
import time
from typing import List

from harness_config import (
    MYSQL_PORT,
    POSTGRES_PORT,
    REDIS_PORT,
    REPO_ROOT,
    S3_PORT,
    G,
    R,
    Y,
    log,
)

# Dedicated process-compose control port (avoids ncps :8501 / pprof :7501).
PC_PORT = int(os.environ.get("NCPS_E2E_PC_PORT", "8513"))

_REQUIRED_PORTS = [S3_PORT, POSTGRES_PORT, MYSQL_PORT, REDIS_PORT]


def _port_open(port: int, host: str = "127.0.0.1") -> bool:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(0.5)
    try:
        s.connect((host, port))
        return True
    except OSError:
        return False
    finally:
        s.close()


def _all_ports_ready(ports: List[int]) -> bool:
    return all(_port_open(p) for p in ports)


def _parse_process_names(raw: str) -> List[str]:
    """Extract process names from `process list -o json`.

    Tolerates both a top-level JSON array and a ``{"data": [...]}`` envelope, and
    returns ``[]`` on any malformed/empty input (diagnostics are best-effort).
    """
    try:
        data = json.loads(raw)
    except (ValueError, TypeError):
        return []
    if isinstance(data, dict):
        data = data.get("data", [])
    if not isinstance(data, list):
        return []
    return [p["name"] for p in data if isinstance(p, dict) and "name" in p]


class Deps:
    """Manages the fixed-port dev backends for a run."""

    def __init__(self, *, needs_redis: bool = False):
        # Redis is only required for staging scenarios; always include the core three.
        self.ports = list(_REQUIRED_PORTS) if needs_redis else [
            S3_PORT,
            POSTGRES_PORT,
            MYSQL_PORT,
        ]
        self._started = False

    # 300s (not 120s): a cold all-backend boot — postgres `initdb`, mariadb
    # `mariadb-install-db`, garage layout, each into a fresh `mktemp` data dir —
    # can exceed two minutes on a resource-constrained CI runner.
    def ensure_up(self, timeout: int = 300) -> None:
        if _all_ports_ready(self.ports):
            log("deps: backends already reachable; leaving them as-is", Y)
            return
        log(f"deps: starting fixed-port backends (pc port {PC_PORT})...", G)
        subprocess.run(
            [
                "nix",
                "run",
                ".#deps",
                "--",
                "up",
                "--detached",
                "--tui=false",
                "-p",
                str(PC_PORT),
            ],
            cwd=REPO_ROOT,
            check=True,
        )
        self._started = True
        deadline = time.time() + timeout
        while time.time() < deadline:
            if _all_ports_ready(self.ports):
                log("deps: all services ready", G)
                return
            time.sleep(2)
        # Surface which backend is unhealthy instead of an opaque timeout, so a
        # CI failure is diagnosable from the logs alone.
        self._dump_diagnostics()
        raise RuntimeError(f"deps: services not ready within {timeout}s")

    def _dump_diagnostics(self) -> None:
        """Best-effort dump of process-compose state + per-process logs.

        Never raises: a diagnostics failure must not mask the readiness error.
        """
        log("deps: services not ready — process-compose state follows:", R)
        names: List[str] = []
        try:
            # `-o json`: the default `process list` prints a formatted table whose
            # header/border rows would be mis-parsed as process names; JSON is the
            # stable machine-readable form.
            listing = subprocess.run(
                ["nix", "run", ".#deps", "--", "process", "list", "-o", "json", "-p", str(PC_PORT)],
                cwd=REPO_ROOT,
                check=False,
                capture_output=True,
                text=True,
                timeout=30,
            )
            log((listing.stdout or "") + (listing.stderr or ""), R)
            names = _parse_process_names(listing.stdout or "")
        except Exception as e:  # noqa: BLE001 — diagnostics are best-effort
            log(f"deps: could not list processes: {e}", R)
        for name in names:
            try:
                logs = subprocess.run(
                    ["nix", "run", ".#deps", "--", "process", "logs", name, "--tail", "100", "-p", str(PC_PORT)],
                    cwd=REPO_ROOT,
                    check=False,
                    capture_output=True,
                    text=True,
                    timeout=20,
                )
                log(f"deps: --- {name} logs ---\n{(logs.stdout or '')}{(logs.stderr or '')}", R)
            except Exception as e:  # noqa: BLE001 — diagnostics are best-effort
                log(f"deps: could not fetch logs for {name}: {e}", R)

    def teardown(self) -> None:
        if not self._started:
            return
        log(f"deps: stopping backends started by this run (pc port {PC_PORT})...", Y)
        try:
            subprocess.run(
                ["nix", "run", ".#deps", "--", "down", "-p", str(PC_PORT)],
                cwd=REPO_ROOT,
                check=False,
                timeout=120,
            )
        except Exception as e:  # noqa: BLE001 — teardown is best-effort
            log(f"deps: teardown error (ignored): {e}", R)
        self._started = False
