#!/usr/bin/env python3

import argparse
import asyncio
import hashlib
import json
import os
import subprocess
import sys
import time
from typing import List

import httpx

STATE_FILE_PATH = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))),
    "var",
    "ncps",
    "state.json",
)

TTFB_TIMEOUT_SECONDS = 180.0  # Adjust this value as needed


def nix_hash_to_hex(hash_str: str) -> str:
    """
    Convert a Nix hash (SRI format like sha256:nix32hash) to hex using nix-hash.

    This handles both nix32 and hex formats:
    - sha256:nix32hash -> converts nix32 to hex
    - sha256:hexhash -> passes through as hex
    - sha256:sha256:hex -> extracts inner hex
    """
    if not hash_str:
        return ""

    # Parse the SRI format
    if not hash_str.startswith("sha256:"):
        return hash_str

    encoded = hash_str[7:]  # Remove "sha256:" prefix

    # Handle double-encoded format: sha256:sha256:hex
    if encoded.startswith("sha256:"):
        return encoded[7:]  # Extract inner hex

    # Otherwise, it's nix32 format - convert to hex using nix-hash
    try:
        result = subprocess.run(
            ["nix-hash", "--sri", "--to-base16", hash_str],
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        # Fallback: return as-is if nix-hash is not available
        return encoded


def parse_narinfo(text: str) -> dict:
    """Parse narinfo text and extract relevant fields."""
    info = {}
    for line in text.splitlines():
        if ": " in line:
            key, value = line.split(": ", 1)
            info[key.strip()] = value.strip()
    return info


def get_expected_hash_info(info: dict) -> tuple[str, str]:
    """Get the expected hash and type from narinfo."""
    compression = info.get("Compression", "").lower()

    # Determine which hash to use
    if compression == "none":
        # When compression is none, FileHash is expected empty, use NarHash
        expected_hash = info.get("NarHash", "")
        hash_type = "NarHash"
    else:
        # In all other cases, FileHash is used/required
        expected_hash = info.get("FileHash", "")
        hash_type = "FileHash"

    return expected_hash, hash_type


async def verify_nar_hash(client: httpx.AsyncClient, nar_url: str, info: dict) -> tuple[bool, str, str]:
    """
    Download NAR and verify its hash matches the narinfo.

    Returns:
        tuple of (passed: bool, expected_hex: str, actual_hash: str)
    """
    expected_hash, hash_type = get_expected_hash_info(info)

    if not expected_hash:
        return True, "", ""

    # Parse the expected hash to get hex digest
    expected_hex = nix_hash_to_hex(expected_hash)

    try:
        # Download the NAR
        resp = await client.get(nar_url)
        resp.raise_for_status()

        # Compute SHA256 hash of the content
        actual_hash = hashlib.sha256(resp.content).hexdigest()

        # Compare
        passed = actual_hash == expected_hex
        return passed, expected_hex, actual_hash
    except Exception as e:
        # Return empty strings to indicate unable to verify
        return True, "", ""


def get_urls_from_state_file() -> List[str]:
    """Read running instance URLs from run.py's state file."""
    try:
        with open(STATE_FILE_PATH) as f:
            data = json.load(f)
        return [
            f"http://127.0.0.1:{inst['port']}" for inst in data.get("instances", [])
        ]
    except (FileNotFoundError, KeyError, json.JSONDecodeError):
        return []


async def fetch_with_verification(client, base_url, path, narinfo, do_verify: bool):
    """Fetch NAR and optionally verify its hash."""
    url = f"{base_url}/{path}"
    start_time = time.perf_counter()
    ttfb = None

    try:
        # Use streaming to capture the moment the first byte arrives
        async with client.stream("GET", url) as response:
            # aiter_bytes() triggers the read, which is subject to the 'read' timeout
            content = bytearray()
            async for chunk in response.aiter_bytes():
                content.extend(chunk)
                if ttfb is None:
                    ttfb = time.perf_counter() - start_time

            total_time = time.perf_counter() - start_time

            result = {
                "url": base_url,
                "ttfb": f"{ttfb:.4f}s" if ttfb else "N/A",
                "total": f"{total_time:.4f}s",
                "status": response.status_code,
                "hash_passed": None,
                "expected_hash": "",
                "actual_hash": "",
            }

            # Verify hash if requested
            if do_verify:
                nar_url = f"{base_url}/{path}"
                passed, expected, actual = await verify_nar_hash(client, nar_url, narinfo)
                result["hash_passed"] = "PASSED" if passed else "FAILED"
                result["expected_hash"] = expected
                result["actual_hash"] = actual

            return result
    except httpx.TimeoutException:
        return {
            "url": base_url,
            "error": f"Timeout: No response within {TTFB_TIMEOUT_SECONDS}s",
            "hash_passed": None,
        }
    except Exception as e:
        return {"url": base_url, "error": str(e), "hash_passed": None}


async def main():
    parser = argparse.ArgumentParser(
        description="Measure NAR latency across ncps instances."
    )
    parser.add_argument("hash", help="The Nix store hash (e.g., 9cmq42r...)")
    parser.add_argument(
        "--ncps-url",
        action="append",
        help="Base URL of an ncps instance (can be specified multiple times)",
    )
    parser.add_argument(
        "--no-verify",
        action="store_true",
        help="Skip NAR hash verification",
    )
    args = parser.parse_args()

    default_urls = get_urls_from_state_file()
    target_urls = [u.rstrip("/") for u in (args.ncps_url or default_urls)]

    if not target_urls:
        print(
            "error: No ncps instances found (state file not present and no --ncps-url given)."
        )
        sys.exit(1)

    narinfo_url = f"{target_urls[0]}/{args.hash}.narinfo"

    # Define the timeout structure. 'read' specifically limits the TTFB.
    timeout = httpx.Timeout(connect=5.0, read=TTFB_TIMEOUT_SECONDS, write=5.0, pool=5.0)

    async with httpx.AsyncClient(timeout=timeout) as client:
        # 1. Fetch narinfo from first instance
        try:
            resp = await client.get(narinfo_url)
            resp.raise_for_status()
        except Exception as e:
            print(f"Error fetching narinfo: {e}")
            sys.exit(1)

        # 2. Parse narinfo fields
        narinfo = parse_narinfo(resp.text)

        # 3. Parse URL entry
        nar_path = narinfo.get("URL", "")
        if not nar_path:
            print("Could not find 'URL' entry in narinfo.")
            sys.exit(1)

        # Print what we're testing
        do_verify = not args.no_verify
        if do_verify:
            expected_hash, hash_type = get_expected_hash_info(narinfo)
            expected_hex = nix_hash_to_hex(expected_hash)
            print(f"Testing NAR: {nar_path}")
            print(f"  Expected hash ({hash_type}): {expected_hex}\n")
        else:
            print(f"Testing NAR: {nar_path}")
            print("  Skipping hash verification (--no-verify)\n")

        # 4. Call in parallel across all instances with optional verification
        tasks = [fetch_with_verification(client, url, nar_path, narinfo, do_verify) for url in target_urls]
        results = await asyncio.gather(*tasks)

        # 5. Report results
        url_width = max(len(r.get("url", "")) for r in results)

        if do_verify:
            print(f"{'URL':<{url_width}} | {'TTFB':<10} | {'Total Time':<12} | {'Status':<6} | {'Hash'}")
            print("-" * (url_width + 50))
        else:
            print(f"{'URL':<{url_width}} | {'TTFB':<10} | {'Total Time':<12} | {'Status'}")
            print("-" * (url_width + 40))

        failed_hashes = []
        for r in results:
            if "error" in r:
                print(f"{r['url']:<{url_width}} | ERROR: {r['error']}")
            else:
                if do_verify:
                    status = r.get("status", "")
                    hash_ver = r.get("hash_passed", "N/A")
                    print(f"{r['url']:<{url_width}} | {r['ttfb']:<10} | {r['total']:<12} | {status:<6} | {hash_ver}")
                    if hash_ver == "FAILED":
                        failed_hashes.append(r)
                else:
                    print(f"{r['url']:<{url_width}} | {r['ttfb']:<10} | {r['total']:<12} | {r['status']}")

        # 6. If there are hash failures, print details
        if failed_hashes:
            print("\nHash verification failures:")
            print(f"{'URL':<{url_width}} | {'Expected':<64} | {'Actual'}")
            print("-" * (url_width + 80))
            for r in failed_hashes:
                print(f"{r['url']:<{url_width}} | {r['expected_hash']:<64} | {r['actual_hash']}")


if __name__ == "__main__":
    asyncio.run(main())
