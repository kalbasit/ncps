"""The mode-adapter interface shared by phase drivers.

A ``Deployment`` brings ncps up for a scenario and exposes the seams phase
drivers need (replica URLs, restart with changed CDC flags, run a CLI
subcommand, query the DB, read logs). ``LocalDeployment`` drives
``dev-scripts/run.py``; ``KubernetesDeployment`` drives Kind + Helm. Phase
drivers depend only on this protocol plus :class:`client.Client` and
:class:`db.DBAccess`.
"""

from __future__ import annotations

from typing import List, Optional, Protocol, Tuple

from client import Client
from db import DBAccess


class Deployment(Protocol):
    def provision(self) -> None:
        """Bring ncps up for the scenario (idempotent for the run)."""
        ...

    def replica_urls(self) -> List[str]:
        """Base URLs of each ncps replica."""
        ...

    def client(self, replica: int = 0) -> Client:
        """A :class:`Client` bound to one replica (0-indexed)."""
        ...

    def restart(self, *, cdc: bool = False, lazy: bool = False) -> None:
        """Stop and restart with changed CDC serve flags (drain lifecycle)."""
        ...

    def stop(self) -> None:
        """Stop the running instance(s) without restarting (drain prep)."""
        ...

    def start(self, *, cdc: bool = False, lazy: bool = False) -> None:
        """Start fresh instance(s) preserving data (no clean)."""
        ...

    def run_subcommand(
        self, subcmd: str, extra: Optional[List[str]] = None, timeout: int = 300
    ) -> Tuple[int, str]:
        """Run `ncps <subcmd>` with the scenario's db + storage flags."""
        ...

    def db(self) -> DBAccess:
        """DB access for invariant assertions."""
        ...

    def logs(self, replica: int = 0) -> str:
        """Captured log text for a replica (for activation assertions)."""
        ...

    def teardown(self) -> None:
        """Always called — success or failure — to release resources."""
        ...
