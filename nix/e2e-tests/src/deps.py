"""Fixed-port backing-services lifecycle (Garage/S3, PG, MariaDB, Redis).

Replaces the former ``test-*-auto.sh`` wrappers: starts ``nix run .#deps`` on a
dedicated process-compose control port, waits for the fixed ports, and — per the
spec — stops only the services this harness started. If the ports are already
reachable, an externally-managed stack is assumed and left untouched.
"""

from __future__ import annotations

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

    def ensure_up(self, timeout: int = 120) -> None:
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
        raise RuntimeError(f"deps: services not ready within {timeout}s")

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
