#!/usr/bin/env python3
"""
Profile per-derivation wall-clock for entries in the flake's `checks` attrset.

For each selected check, runs `nix build .#checks.<system>.<name> -L --rebuild`
and records elapsed wall time. The `--rebuild` flag forces re-execution even if
the derivation is already in the local store, so the timings reflect the cost
of actually building / running the check — which is what
`nix flake check` pays on a CI cold cache (modulo cache-pull time, which is
hardware-bound and not affected by topology changes).

Outputs a markdown-style ranked table to stdout and, if `--out` is given, also
writes it to that file. Failing derivations are reported but do not abort the
run.

Usage:
    ./dev-scripts/profile-flake-checks.py \\
        [--system x86_64-linux] \\
        [--checks atlas-sum-check,ent-lint-check,...] \\
        [--exclude default,deps,...] \\
        [--out openspec/changes/lean-flake-check/baseline-timings.txt]
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path

# Checks that are not quality gates (devshells, docker images, helpers) and
# should be skipped by default. Override with --exclude=''.
DEFAULT_EXCLUDES = {
    "default",  # alias of packages.ncps
    "deps",  # process-compose dev deps
    "docker",  # runtime image, not a check
    "docker-dev",  # dev image, not a check
    "k8s-tests",  # CLI tool, not a check
    "push-docker-image",  # publish action, not a check
    "treefmt",  # devshell, exercised by formatter
    "update-cu-base",  # CLI tool, not a check
}

# Manual annotation of which backends each known check starts in preCheck.
# Update as topology changes. Unknown checks render as `?`.
BACKEND_MAP: dict[str, list[str]] = {
    "atlas-sum-check": [],
    "ent-codegen-drift-check": [],
    "ent-lint-check": [],
    "golangci-lint-check": [],
    "helm-unittest-check": [],
    "ncps": ["garage", "postgres", "mariadb", "redis"],
    "schema-equivalence-check": ["postgres", "mariadb"],
}


@dataclass
class Result:
    name: str
    seconds: float
    ok: bool
    backends: list[str]


def list_checks(system: str) -> list[str]:
    out = subprocess.run(
        ["nix", "flake", "show", "--json"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    data = json.loads(out)
    return sorted(data["checks"][system].keys())


def delete_top_output(system: str, name: str) -> None:
    """Delete only the top-level output path for this check.

    Leaves intermediate dependencies (e.g. *-go-modules) in the store, so the
    timed build measures the same work CI's cold cache does after Cachix has
    substituted the deps.
    """
    attr = f".#checks.{system}.{name}"
    proc = subprocess.run(
        ["nix", "eval", "--raw", attr],
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0 or not proc.stdout.startswith("/nix/store/"):
        return
    out_path = proc.stdout.strip()
    subprocess.run(
        ["nix", "store", "delete", "--ignore-liveness", out_path],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )


def build_one(system: str, name: str) -> tuple[float, bool]:
    delete_top_output(system, name)
    attr = f".#checks.{system}.{name}"
    start = time.monotonic()
    proc = subprocess.run(
        ["nix", "build", attr, "-L", "--no-link"],
        stdout=sys.stderr,
        stderr=sys.stderr,
    )
    return time.monotonic() - start, proc.returncode == 0


def fmt_secs(s: float) -> str:
    m, sec = divmod(int(s), 60)
    return f"{m}m{sec:02d}s" if m else f"{sec}s"


def render(results: list[Result], system: str) -> str:
    results = sorted(results, key=lambda r: -r.seconds)
    total = sum(r.seconds for r in results)
    lines = []
    lines.append(f"# Flake check timings ({system})")
    lines.append("")
    lines.append(f"Total wall-clock (sum of per-derivation runs): {fmt_secs(total)}")
    lines.append("")
    lines.append("| Derivation | Wall-clock | Backends started | Status |")
    lines.append("| --- | ---:| --- | --- |")
    for r in results:
        backends = ", ".join(r.backends) if r.backends else "none"
        status = "ok" if r.ok else "FAILED"
        lines.append(f"| `{r.name}` | {fmt_secs(r.seconds)} | {backends} | {status} |")
    return "\n".join(lines) + "\n"


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--system", default=os.environ.get("NIX_SYSTEM", "x86_64-linux"))
    p.add_argument(
        "--checks",
        default="",
        help="Comma-separated subset; default = all checks minus --exclude.",
    )
    p.add_argument(
        "--exclude",
        default=",".join(sorted(DEFAULT_EXCLUDES)),
        help="Comma-separated checks to skip.",
    )
    p.add_argument("--out", type=Path, default=None)
    args = p.parse_args()

    excludes = {s for s in args.exclude.split(",") if s}
    if args.checks:
        targets = [s for s in args.checks.split(",") if s]
    else:
        targets = [c for c in list_checks(args.system) if c not in excludes]

    print(f"Profiling {len(targets)} check(s) on {args.system}:", file=sys.stderr)
    for name in targets:
        print(f"  - {name}", file=sys.stderr)

    results: list[Result] = []
    for i, name in enumerate(targets, 1):
        print(f"\n[{i}/{len(targets)}] building {name} ...", file=sys.stderr)
        secs, ok = build_one(args.system, name)
        print(f"  -> {fmt_secs(secs)} ({'ok' if ok else 'FAILED'})", file=sys.stderr)
        results.append(
            Result(name=name, seconds=secs, ok=ok, backends=BACKEND_MAP.get(name, ["?"]))
        )

    report = render(results, args.system)
    print(report)
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(report)
        print(f"\nWrote {args.out}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
