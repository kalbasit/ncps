#!/usr/bin/env python3
import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from typing import List, Tuple

STATE_FILE_PATH = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "var",
    "ncps",
    "state.json",
)


def get_urls_from_state_file() -> List[str]:
    """Read running instance URLs from run.py's state file."""
    try:
        with open(STATE_FILE_PATH) as f:
            data = json.load(f)
        return [
            f"http://localhost:{inst['port']}" for inst in data.get("instances", [])
        ]
    except (FileNotFoundError, KeyError, json.JSONDecodeError):
        return []


def get_physical_temp_dir() -> str:
    """Creates a temp dir and resolves symlinks (crucial for macOS /var)."""
    tmp = tempfile.mkdtemp(prefix="nix-store.")
    return os.path.realpath(tmp)


def probe_ncpses(urls: List[str]) -> List[Tuple[str, str]]:
    """Determines which ncps is online and fetches their public keys."""
    active_ncpses = []
    for url in urls:
        url = url.rstrip("/")
        try:
            # Check if cache is alive
            with urllib.request.urlopen(f"{url}/nix-cache-info", timeout=2):
                # Fetch pubkey
                with urllib.request.urlopen(f"{url}/pubkey", timeout=2) as r:
                    pubkey = r.read().decode("utf-8").strip()
                    active_ncpses.append((url, pubkey))
                    print(f"✅ Found active ncps: {url}")
        except Exception:
            print(f"── Ignoring offline ncps: {url}")
    return active_ncpses


def main():
    parser = argparse.ArgumentParser(
        description="Multi-cache Nix build reproduction script"
    )
    parser.add_argument("packages", nargs="+", help="Nix flakeref packages to build")
    parser.add_argument(
        "--leave-nix-store",
        action="store_true",
        help="Do not delete the temp store on exit",
    )
    parser.add_argument(
        "--ncps-url",
        action="append",
    )
    args = parser.parse_args()

    default_urls = get_urls_from_state_file()
    active_configs = probe_ncpses(args.ncps_url or default_urls)

    if not active_configs:
        if not args.ncps_url and not default_urls:
            print(
                "error: No ncps instances found (state file not present and no --ncps-url given)."
            )
        else:
            print("error: No active caches found.")
        sys.exit(1)

    # 1. Define temp_store before using it
    temp_store = get_physical_temp_dir()
    print(f"Nix store directory: {temp_store}")

    substituters = " ".join([c[0] for c in active_configs])

    # We only use the keys from the active caches we found.
    # We deliberately exclude cache.nixos.org keys to test strict isolation.
    pubkeys = " ".join([c[1] for c in active_configs])

    cmd = [
        "nix",
        "run",
        "nixpkgs#nixVersions.stable",
        "--",
        "build",
        "--refresh",
        "--store",
        temp_store,
        "--no-link",
        # OVERRIDE: This replaces the default https://cache.nixos.org
        "--substituters",
        substituters,
        # OVERRIDE: This empties any global extra substituters (like flake settings)
        "--option",
        "extra-substituters",
        "",
        "--trusted-public-keys",
        pubkeys,
        # OVERRIDE: This empties any global extra substituters (like flake settings)
        "--extra-trusted-public-keys",
        "",
    ] + args.packages

    try:
        print(f"Running: {' '.join(cmd)}")
        # 2. Actually run the command!
        subprocess.run(cmd, check=True)

    except subprocess.CalledProcessError as e:
        print(f"\nBuild failed with exit code {e.returncode}")
        sys.exit(e.returncode)
    finally:
        if not args.leave_nix_store:
            print(f"Cleaning up {temp_store}...")
            shutil.rmtree(temp_store, ignore_errors=True)
        else:
            print(f"Leaving Nix store at: {temp_store}")


if __name__ == "__main__":
    main()
