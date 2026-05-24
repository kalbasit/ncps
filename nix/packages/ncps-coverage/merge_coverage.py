#!/usr/bin/env python3
"""Merge multiple Go coverage profiles into one (sum hit counts).

Usage: merge_coverage.py <cover.out> [<cover.out>...]
Writes the merged profile to stdout.

Equivalent to `gocovmerge` (https://github.com/wadey/gocovmerge), inlined
here because gocovmerge is not packaged in nixpkgs.
"""

from __future__ import annotations

import sys


def main(paths: list[str]) -> int:
    counts: dict[str, list[int]] = {}  # "<file>:<range>" -> [block_count, total_hit_count]
    mode: str | None = None

    for path in paths:
        with open(path) as f:
            header = f.readline().rstrip("\n")
            if mode is None:
                mode = header
            for line in f:
                line = line.rstrip("\n")
                if not line:
                    continue
                # "<file>:<line.col,line.col> <block_count> <hit_count>"
                parts = line.rsplit(None, 2)
                if len(parts) != 3:
                    continue
                key, block_count, hit_count = parts[0], int(parts[1]), int(parts[2])
                entry = counts.get(key)
                if entry is None:
                    counts[key] = [block_count, hit_count]
                else:
                    entry[1] += hit_count

    print(mode or "mode: atomic")
    for key in sorted(counts):
        block_count, hit_count = counts[key]
        print(f"{key} {block_count} {hit_count}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
