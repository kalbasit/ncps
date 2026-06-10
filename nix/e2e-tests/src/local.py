"""Local deployment: drives ncps through ``dev-scripts/run.py``.

run.py starts each replica under watchexec+direnv, writes per-replica logs to
``var/log/ncps-<port>.log``, assigns HTTP ports ``BASE_PORT + (i-1)``, and blocks
after spawning — so it is run as a background process group the harness signals
on teardown. Local mode therefore requires the dev shell toolchain (go, dbmate,
direnv, watchexec), i.e. run it via ``task test:e2e`` or inside ``nix develop``.
"""

from __future__ import annotations

import json
import os
import signal
import socket
import subprocess
import time
import urllib.request
from typing import List, Optional, Tuple

from client import Client
from db import DBAccess
from harness_config import (
    BASE_PORT,
    DB_URLS,
    LOG_DIR,
    PYTHON,
    REPO_ROOT,
    RUN_PY,
    G,
    R,
    Y,
    log,
    storage_flags,
)


class LocalDeployment:
    """A run.py-backed deployment of one scenario."""

    def __init__(self, scenario):
        self.scenario = scenario
        self.replicas = max(1, scenario.replicas)
        self.storage = scenario.storage
        self.database = scenario.database  # run.py vocabulary
        self.staging = scenario.staging
        # Staging needs a distributed locker; HA implies one too.
        self.locker = "redis" if (self.staging or self.replicas > 1) else "local"
        self._proc: Optional[subprocess.Popen] = None
        self._harness_log = None
        self._cdc = scenario.cdc in ("eager", "lazy")
        self._lazy = scenario.cdc == "lazy"

    # -- lifecycle -------------------------------------------------------------

    def _ports(self) -> List[int]:
        return [BASE_PORT + i for i in range(self.replicas)]

    def _start(self, *, clean: bool, cdc: bool, lazy: bool) -> None:
        args = [
            PYTHON,
            RUN_PY,
            "--db",
            self.database,
            "--storage",
            self.storage,
            "--replicas",
            str(self.replicas),
            "--locker",
            self.locker,
        ]
        if clean:
            args.append("--clean")
        if lazy:
            args.append("--enable-lazy-cdc")
        elif cdc:
            args.append("--enable-cdc")
        if self.staging:
            args.append("--inflight-staging")

        os.makedirs(LOG_DIR, exist_ok=True)
        harness_log_path = os.path.join(LOG_DIR, "e2e-run.py.log")
        self._harness_log = open(harness_log_path, "w", encoding="utf-8")
        log(
            f"local: run.py db={self.database} storage={self.storage} "
            f"replicas={self.replicas} locker={self.locker} cdc={cdc} lazy={lazy} "
            f"staging={self.staging} clean={clean}",
            G,
        )
        # start_new_session isolates the child in its own process group so
        # teardown can signal the whole tree via os.killpg.
        self._proc = subprocess.Popen(
            args,
            cwd=REPO_ROOT,
            stdout=self._harness_log,
            stderr=subprocess.STDOUT,
            start_new_session=True,
        )
        self._wait_all_ready()

    def _wait_all_ready(self, timeout: int = 240) -> None:
        deadline = time.time() + timeout
        pending = set(self._ports())
        while time.time() < deadline:
            if self._proc.poll() is not None:
                self._dump_logs()
                raise RuntimeError(
                    f"local: run.py exited with code {self._proc.returncode} "
                    "before all replicas became ready (see var/log/e2e-run.py.log)"
                )
            for port in list(pending):
                try:
                    url = f"http://127.0.0.1:{port}/nix-cache-info"
                    with urllib.request.urlopen(url, timeout=2) as r:
                        if r.status == 200:
                            pending.discard(port)
                except Exception:
                    pass
            if not pending:
                log(f"local: all {self.replicas} replica(s) ready", G)
                return
            time.sleep(1)
        self._dump_logs()
        raise RuntimeError(f"local: replicas not ready within {timeout}s: {sorted(pending)}")

    def _dump_logs(self) -> None:
        """Best-effort: echo run.py's harness log and each replica's ncps log so
        a CI failure shows *why* ncps did not come up. Never raises."""
        try:
            if self._harness_log is not None:
                self._harness_log.flush()
        except Exception:  # noqa: BLE001 — diagnostics are best-effort
            pass
        paths = [os.path.join(LOG_DIR, "e2e-run.py.log")]
        paths += [os.path.join(LOG_DIR, f"ncps-{p}.log") for p in self._ports()]
        for path in paths:
            try:
                with open(path, encoding="utf-8", errors="replace") as f:
                    content = f.read().strip()
            except OSError:
                content = "(no log file)"
            log(f"local: --- {os.path.basename(path)} ---\n{content or '(empty)'}", R)

    def _stop(self) -> None:
        if self._proc is not None and self._proc.poll() is None:
            try:
                os.killpg(os.getpgid(self._proc.pid), signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                self._proc.wait(timeout=30)
            except subprocess.TimeoutExpired:
                log("local: SIGTERM timeout, sending SIGKILL", R)
                try:
                    os.killpg(os.getpgid(self._proc.pid), signal.SIGKILL)
                except ProcessLookupError:
                    pass
                self._proc.wait(timeout=5)
        self._proc = None
        if self._harness_log is not None:
            self._harness_log.close()
            self._harness_log = None
        self._wait_ports_closed()

    def _wait_ports_closed(self, timeout: int = 20) -> None:
        deadline = time.time() + timeout
        for port in self._ports():
            while time.time() < deadline:
                s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                s.settimeout(0.5)
                try:
                    s.connect(("127.0.0.1", port))
                    s.close()
                    time.sleep(0.5)
                except OSError:
                    break

    # -- Deployment protocol ---------------------------------------------------

    def provision(self) -> None:
        # Fresh data on first provision so each run is deterministic.
        self._start(clean=True, cdc=self._cdc, lazy=self._lazy)

    def replica_urls(self) -> List[str]:
        return [f"http://127.0.0.1:{p}" for p in self._ports()]

    def client(self, replica: int = 0) -> Client:
        return Client(self.replica_urls()[replica])

    def restart(self, *, cdc: bool = False, lazy: bool = False) -> None:
        self._stop()
        self._cdc, self._lazy = cdc or lazy, lazy
        # Preserve data across restart (no --clean) for the drain lifecycle.
        self._start(clean=False, cdc=cdc or lazy, lazy=lazy)

    def stop(self) -> None:
        """Stop the running instance(s) without restarting.

        The CDC drain lifecycle stops ncps before `migrate-chunks-to-nar
        --force-reclaim`, which needs exclusive storage/DB access.
        """
        self._stop()

    def start(self, *, cdc: bool = False, lazy: bool = False) -> None:
        """Start fresh instance(s) preserving data (no --clean)."""
        self._cdc, self._lazy = cdc or lazy, lazy
        self._start(clean=False, cdc=cdc or lazy, lazy=lazy)

    def clean_restart(self, *, cdc: bool = False, lazy: bool = False) -> None:
        """Stop and restart with wiped data (--clean) — e.g. between staging windows."""
        self._stop()
        self._cdc, self._lazy = cdc or lazy, lazy
        self._start(clean=True, cdc=cdc or lazy, lazy=lazy)

    def read_state(self) -> dict:
        """Parse var/ncps/state.json (effective locker/staging/instances)."""
        from harness_config import STATE_FILE

        with open(STATE_FILE, encoding="utf-8") as fp:
            return json.load(fp)

    def run_subcommand(self, subcmd: str, extra=None, timeout: int = 300) -> Tuple[int, str]:
        with_temp = subcmd == "migrate-chunks-to-nar"
        cmd = ["go", "run", ".", subcmd, f"--cache-database-url={DB_URLS[self.database]}"]
        if with_temp:
            from harness_config import TEMP_PATH

            cmd.append(f"--cache-temp-path={TEMP_PATH}")
        cmd += storage_flags(self.storage)
        if extra:
            cmd += extra
        redacted = [
            "--cache-storage-s3-secret-access-key=***"
            if a.startswith("--cache-storage-s3-secret-access-key=")
            else a
            for a in cmd
        ]
        log(f"local: {' '.join(redacted)}", G)
        r = subprocess.run(
            cmd, cwd=REPO_ROOT, capture_output=True, text=True, timeout=timeout
        )
        return r.returncode, r.stdout + r.stderr

    def db(self) -> DBAccess:
        return DBAccess(self.database)

    def logs(self, replica: int = 0) -> str:
        port = self._ports()[replica]
        path = os.path.join(LOG_DIR, f"ncps-{port}.log")
        try:
            with open(path, "r", encoding="utf-8", errors="replace") as f:
                return f.read()
        except FileNotFoundError:
            return ""

    def teardown(self) -> None:
        try:
            self._stop()
        except Exception as e:  # noqa: BLE001 — teardown is best-effort
            log(f"local: teardown error (ignored): {e}", Y)
