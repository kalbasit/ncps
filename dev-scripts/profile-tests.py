#!/usr/bin/env python3
"""
Profile Go test execution time.

Runs `go test -json -race -count=1` against a chosen package set and emits two
ranked tables:

  1. Per-package total wall time.
  2. Per-test wall time (parent + subtests), filtered to `Elapsed > THRESHOLD`.

Output is written to stdout in plain text. The script exits non-zero only if
`go test` itself fails to run; test failures themselves do not affect the exit
code, because the goal here is to gather timings, not to gate CI. Use
`go test` directly for pass/fail gating.

Usage:
    ./dev-scripts/profile-tests.py [--packages ./...] [--threshold 0.5] \
                                   [--out openspec/changes/.../baseline.txt]

Integration tests participate when their env vars are set
(`eval "$(enable-integration-tests)"` in the Nix dev shell). Without those env
vars, integration tests show up as skipped and do not contribute to wall time.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path


@dataclass
class PackageStats:
    name: str
    elapsed: float = 0.0
    passed: int = 0
    failed: int = 0
    skipped: int = 0
    # Per-test (incl. subtests) elapsed times for tests reported under this package.
    tests: dict[str, float] = field(default_factory=dict)


def parse_go_test_json(stream) -> dict[str, PackageStats]:
    """Parse `go test -json` event stream into per-package stats."""
    packages: dict[str, PackageStats] = defaultdict(lambda: PackageStats(name=""))

    for raw in stream:
        raw = raw.strip()
        if not raw:
            continue
        try:
            event = json.loads(raw)
        except json.JSONDecodeError:
            # `go test -json` occasionally emits prelude/build lines that aren't
            # JSON. Skip them rather than aborting the entire run.
            continue

        pkg_name = event.get("Package")
        if not pkg_name:
            continue

        pkg = packages[pkg_name]
        if not pkg.name:
            pkg.name = pkg_name

        action = event.get("Action")
        test_name = event.get("Test")
        elapsed = event.get("Elapsed")

        # Package-level summary event (no "Test" field, action in pass/fail/skip).
        if test_name is None and action in ("pass", "fail", "skip") and elapsed is not None:
            pkg.elapsed = float(elapsed)
            continue

        # Per-test summary event.
        if test_name is not None and action in ("pass", "fail", "skip") and elapsed is not None:
            pkg.tests[test_name] = float(elapsed)
            if action == "pass":
                pkg.passed += 1
            elif action == "fail":
                pkg.failed += 1
            else:
                pkg.skipped += 1

    return packages


def render_report(packages: dict[str, PackageStats], threshold: float) -> str:
    out: list[str] = []

    # --- Per-package ranking ---
    out.append("=" * 80)
    out.append("PER-PACKAGE WALL TIME (ranked, descending)")
    out.append("=" * 80)
    out.append(f"{'elapsed (s)':>12}  {'tests':>6}  {'pass':>5}  {'fail':>5}  {'skip':>5}  package")
    out.append("-" * 80)

    total_elapsed = 0.0
    ranked_pkgs = sorted(packages.values(), key=lambda p: p.elapsed, reverse=True)
    for pkg in ranked_pkgs:
        # Skip packages with no test events at all (e.g., `[no test files]`).
        if not pkg.tests and pkg.elapsed == 0.0:
            continue
        total_elapsed += pkg.elapsed
        n_tests = len(pkg.tests)
        out.append(
            f"{pkg.elapsed:>12.3f}  {n_tests:>6}  {pkg.passed:>5}  {pkg.failed:>5}  {pkg.skipped:>5}  {pkg.name}"
        )

    out.append("-" * 80)
    out.append(f"{total_elapsed:>12.3f}  TOTAL (sum of per-package elapsed)")
    out.append("")

    # --- Per-test ranking (above threshold) ---
    out.append("=" * 80)
    out.append(f"SLOW TESTS (elapsed > {threshold:.3f}s, parent + subtests)")
    out.append("=" * 80)
    out.append(f"{'elapsed (s)':>12}  package / test")
    out.append("-" * 80)

    rows: list[tuple[float, str, str]] = []
    for pkg in packages.values():
        for tname, telapsed in pkg.tests.items():
            if telapsed > threshold:
                rows.append((telapsed, pkg.name, tname))

    rows.sort(key=lambda r: r[0], reverse=True)
    for telapsed, pkg_name, tname in rows:
        out.append(f"{telapsed:>12.3f}  {pkg_name}  {tname}")
    if not rows:
        out.append("(none)")
    out.append("")

    return "\n".join(out)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--packages",
        default="./...",
        help="Go package selector passed to `go test` (default: ./...)",
    )
    parser.add_argument(
        "--threshold",
        type=float,
        default=0.5,
        help="Only show individual tests with elapsed > THRESHOLD seconds (default: 0.5)",
    )
    parser.add_argument(
        "--out",
        type=Path,
        default=None,
        help="Write the rendered report to this path in addition to stdout.",
    )
    parser.add_argument(
        "--no-race",
        action="store_true",
        help="Skip -race (faster, but does NOT match nix flake check). Use only for noise-floor measurements.",
    )
    parser.add_argument(
        "--from-file",
        type=Path,
        default=None,
        help="Parse pre-captured `go test -json` output from this file instead of running `go test`.",
    )
    args = parser.parse_args()

    if args.from_file is not None:
        with args.from_file.open("r", encoding="utf-8") as f:
            packages = parse_go_test_json(f)
    else:
        cmd = ["go", "test", "-json", "-count=1"]
        if not args.no_race:
            cmd.append("-race")
        cmd.append(args.packages)

        print(f"+ {' '.join(cmd)}", file=sys.stderr)
        proc = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=sys.stderr,
            text=True,
            bufsize=1,
        )
        assert proc.stdout is not None
        packages = parse_go_test_json(proc.stdout)
        rc = proc.wait()
        if rc != 0:
            print(f"warning: `go test` exited with status {rc} (timings still collected)", file=sys.stderr)

    report = render_report(packages, threshold=args.threshold)
    print(report)
    if args.out is not None:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(report, encoding="utf-8")
        print(f"wrote {args.out}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    sys.exit(main())
