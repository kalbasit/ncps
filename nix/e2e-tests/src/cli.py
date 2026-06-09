#!/usr/bin/env python3
"""Command-line entrypoint for the unified ncps e2e harness.

Usage:
    e2e --list
    e2e --mode local|kubernetes --scenario <name> [--verbose]

``--mode`` is required for a run and is validated up front. ``--list`` prints
the scenario catalog and needs no mode.
"""

import argparse
import sys

MODES = ("local", "kubernetes")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="e2e",
        description=(
            "Unified ncps end-to-end test harness. Runs a scenario from the "
            "catalog against a local run.py deployment or a Kind/Helm cluster."
        ),
    )
    parser.add_argument(
        "--mode",
        choices=MODES,
        help="Deployment substrate for the run (required unless --list).",
    )
    parser.add_argument(
        "--scenario",
        help="Name of the catalog scenario to run.",
    )
    parser.add_argument(
        "--list",
        action="store_true",
        help="List every catalog scenario with its dimensions and supported modes, then exit.",
    )
    parser.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Verbose output.",
    )
    return parser


def main(argv=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    parser = build_parser()
    args = parser.parse_args(argv)

    # --list short-circuits; it needs neither --mode nor --scenario.
    if args.list:
        from catalog import load_catalog, format_catalog_listing

        print(format_catalog_listing(load_catalog()))
        return 0

    # A run requires both --mode and --scenario.
    if args.mode is None:
        parser.error("--mode is required (choose 'local' or 'kubernetes') unless --list is given")
    if args.scenario is None:
        parser.error("--scenario is required unless --list is given")

    from runner import run_scenario

    return run_scenario(mode=args.mode, scenario_name=args.scenario, verbose=args.verbose)


if __name__ == "__main__":
    raise SystemExit(main())
