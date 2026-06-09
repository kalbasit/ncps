"""Scenario catalog, sourced from ``config.nix``.

The catalog is the single source of truth shared by both modes. It is the same
``config.nix`` the former ``k8s-tests`` CLI used: we materialize it as JSON with
``nix eval --json --file <config> permutations`` (one eval at startup) and load
it here. Each permutation is mapped to a :class:`Scenario` whose harness fields
(``phase``, ``modes``, ``cdc``, ``staging``) are taken from explicit keys when
present and otherwise derived from the existing permutation shape, so the
Kubernetes ``generateValues`` path keeps working unchanged.
"""

from __future__ import annotations

import json
import os
import subprocess
from dataclasses import dataclass, field
from typing import Any, Dict, List

# Features whose deployment shape only exists on Kubernetes; a permutation that
# needs any of them cannot be expressed by the local run.py substrate.
_K8S_ONLY_FEATURES = {"existing-secret", "pod-disruption-budget", "anti-affinity"}

# config.nix uses Helm/chart database vocabulary; run.py uses its own.
_DB_TO_RUNPY = {"postgresql": "postgres", "mysql": "mysql", "sqlite": "sqlite"}

VALID_MODES = ("local", "kubernetes")
VALID_PHASES = ("serve", "cdc-lifecycle", "staging-contention")


def _config_file() -> str:
    """Resolve the catalog path (CONFIG_FILE is set by the Nix wrapper)."""
    explicit = os.environ.get("CONFIG_FILE")
    if explicit:
        return explicit
    repo_root = os.path.dirname(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    )
    for candidate in (
        os.path.join(repo_root, "nix", "e2e-tests", "config.nix"),
        os.path.join(repo_root, "nix", "k8s-tests", "config.nix"),
    ):
        if os.path.exists(candidate):
            return candidate
    raise FileNotFoundError(
        "could not locate config.nix; set CONFIG_FILE to the catalog path"
    )


@dataclass(frozen=True)
class Scenario:
    """A single catalog entry, normalized for both modes."""

    name: str
    description: str
    storage: str  # "local" | "s3"
    database: str  # run.py vocabulary: "sqlite" | "postgres" | "mysql"
    replicas: int
    cdc: str  # "off" | "eager" | "lazy"
    staging: bool
    phase: str  # one of VALID_PHASES
    modes: List[str]  # subset of VALID_MODES
    raw: Dict[str, Any] = field(repr=False, default_factory=dict)

    def supports(self, mode: str) -> bool:
        return mode in self.modes


def _derive_cdc(perm: Dict[str, Any]) -> str:
    explicit = perm.get("cdc")
    if explicit:
        return explicit
    return "eager" if "cdc" in perm.get("features", []) else "off"


def _derive_modes(perm: Dict[str, Any]) -> List[str]:
    explicit = perm.get("modes")
    if explicit:
        return list(explicit)
    features = set(perm.get("features", []))
    storage = perm.get("storage", {})
    database = perm.get("database", {})
    k8s_only = (
        bool(features & _K8S_ONLY_FEATURES)
        or storage.get("useExistingSecret", False)
        or database.get("useExistingSecret", False)
        or perm.get("migration", {}).get("mode") == "job"
    )
    return ["kubernetes"] if k8s_only else ["local", "kubernetes"]


def _derive_phase(perm: Dict[str, Any]) -> str:
    explicit = perm.get("phase")
    if explicit:
        return explicit
    if "cdc-lifecycle" in perm.get("features", []):
        return "cdc-lifecycle"
    return "serve"


def _to_scenario(perm: Dict[str, Any]) -> Scenario:
    db_type = perm.get("database", {}).get("type", "sqlite")
    return Scenario(
        name=perm["name"],
        description=perm.get("description", ""),
        storage=perm.get("storage", {}).get("type", "local"),
        database=_DB_TO_RUNPY.get(db_type, db_type),
        replicas=int(perm.get("replicas", 1)),
        cdc=_derive_cdc(perm),
        staging=bool(perm.get("inflightStaging", {}).get("enabled", False)),
        phase=_derive_phase(perm),
        modes=_derive_modes(perm),
        raw=perm,
    )


def load_catalog(config_file: str | None = None) -> List[Scenario]:
    """Materialize ``config.nix`` and return the scenario list."""
    config_file = config_file or _config_file()
    out = subprocess.check_output(
        ["nix", "eval", "--json", "--file", config_file, "permutations"],
        text=True,
    )
    perms = json.loads(out)
    return [_to_scenario(p) for p in perms]


def find_scenario(name: str, catalog: List[Scenario] | None = None) -> Scenario:
    """Look a scenario up by name, failing fast with the valid names listed."""
    catalog = catalog if catalog is not None else load_catalog()
    for scenario in catalog:
        if scenario.name == name:
            return scenario
    valid = ", ".join(sorted(s.name for s in catalog))
    raise KeyError(f"unknown scenario '{name}'. Valid scenarios: {valid}")


def format_catalog_listing(catalog: List[Scenario]) -> str:
    """Human-readable ``--list`` output: name, dimensions, supported modes."""
    lines = [f"Available e2e scenarios ({len(catalog)}):", ""]
    for s in catalog:
        dims = (
            f"storage={s.storage} db={s.database} replicas={s.replicas} "
            f"cdc={s.cdc} staging={str(s.staging).lower()} phase={s.phase}"
        )
        lines.append(f"  {s.name}")
        lines.append(f"      {dims}")
        lines.append(f"      modes: {', '.join(s.modes)}")
        if s.description:
            lines.append(f"      {s.description}")
        lines.append("")
    return "\n".join(lines).rstrip()
