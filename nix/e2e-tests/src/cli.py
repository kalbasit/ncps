#!/usr/bin/env python3
"""Command-line entrypoint for the unified ncps e2e harness.

Usage:
    e2e --list
    e2e --mode local|kubernetes --scenario <name> [--scenario <name> ...] [--verbose]
    e2e --mode local|kubernetes --all [--verbose]

``--mode`` is required for a run and is validated up front. ``--list`` prints
the scenario catalog and needs no mode. A run selects scenarios either with one
or more ``--scenario`` values (repeatable and/or comma-separated) or with
``--all`` (every catalog scenario); the two are mutually exclusive.
"""

import argparse
import sys

MODES = ("local", "kubernetes")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="e2e",
        description=(
            "Unified ncps end-to-end test harness. Runs one or more scenarios "
            "from the catalog against a local run.py deployment or a Kind/Helm "
            "cluster."
        ),
    )
    parser.add_argument(
        "--mode",
        choices=MODES,
        help="Deployment substrate for the run (required unless --list).",
    )
    parser.add_argument(
        "--scenario",
        action="append",
        metavar="NAME[,NAME...]",
        help=(
            "Catalog scenario to run. Repeatable, and each value may be a "
            "comma-separated list. Mutually exclusive with --all."
        ),
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Run every catalog scenario supporting the chosen mode. "
        "Mutually exclusive with --scenario.",
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


def _split_scenarios(values):
    """Flatten repeated/comma-separated --scenario values into a name list."""
    names = []
    for value in values or []:
        names.extend(part.strip() for part in value.split(",") if part.strip())
    return names


def main(argv=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    parser = build_parser()
    args = parser.parse_args(argv)

    # --list short-circuits; it needs neither --mode nor a selection.
    if args.list:
        from catalog import load_catalog, format_catalog_listing

        print(format_catalog_listing(load_catalog()))
        return 0

    # A run requires --mode and exactly one of --all / --scenario.
    if args.mode is None:
        parser.error("--mode is required (choose 'local' or 'kubernetes') unless --list is given")
    if args.all and args.scenario:
        parser.error("--all and --scenario are mutually exclusive")
    if not args.all and not args.scenario:
        parser.error("one of --scenario or --all is required unless --list is given")

    # None means "every scenario"; otherwise the explicit, flattened list.
    scenario_names = None if args.all else _split_scenarios(args.scenario)

    import runner

    return runner.run_scenarios(
        mode=args.mode, scenario_names=scenario_names, verbose=args.verbose
    )


if __name__ == "__main__":
    raise SystemExit(main())
