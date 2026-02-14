#!/usr/bin/env python3
import argparse
import os
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from typing import List, Tuple


def get_physical_temp_dir() -> str:
    """Creates a temp dir and resolves symlinks (crucial for macOS /var)."""
    tmp = tempfile.mkdtemp(prefix="nix-store.")
    return os.path.realpath(tmp)


def probe_caches(urls: List[str]) -> List[Tuple[str, str]]:
    """Determines which caches are online and fetches their public keys."""
    active_caches = []
    for url in urls:
        url = url.rstrip("/")
        try:
            # Check if cache is alive
            with urllib.request.urlopen(f"{url}/nix-cache-info", timeout=2):
                # Fetch pubkey
                with urllib.request.urlopen(f"{url}/pubkey", timeout=2) as r:
                    pubkey = r.read().decode("utf-8").strip()
                    active_caches.append((url, pubkey))
                    print(f"✅ Found active cache: {url}")
        except Exception:
            print(f"── Ignoring offline cache: {url}")
    return active_caches


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
    args = parser.parse_args()

    default_urls = [
        "http://localhost:8501",
        "http://localhost:8502",
        "http://localhost:8503",
    ]
    active_configs = probe_caches(default_urls)

    if not active_configs:
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
        "--option",
        "require-sigs",
        "false",
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
