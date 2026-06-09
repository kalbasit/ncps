"""Phase drivers — the feature-behavior bodies a scenario runs.

Each phase exposes ``run(deployment, scenario)`` and raises
``harness_config.AssertionFailure`` on a failed invariant. Imports are lazy so a
mode/phase that is not yet wired does not break unrelated runs.
"""

from __future__ import annotations


def get_phase(name: str):
    if name == "serve":
        from phases.serve import run

        return run
    if name == "cdc-lifecycle":
        from phases.cdc_lifecycle import run

        return run
    if name == "staging-contention":
        from phases.staging_contention import run

        return run
    raise ValueError(f"unknown phase: {name!r}")
