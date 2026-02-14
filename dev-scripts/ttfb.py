#!/usr/bin/env python3

import argparse
import asyncio
import json
import os
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

TTFB_TIMEOUT_SECONDS = 10.0  # Adjust this value as needed


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


async def fetch_metrics(client, base_url, path):
    url = f"{base_url}/{path}"
    start_time = time.perf_counter()
    ttfb = None

    try:
        # Use streaming to capture the moment the first byte arrives
        async with client.stream("GET", url) as response:
            # aiter_bytes() triggers the read, which is subject to the 'read' timeout
            async for _ in response.aiter_bytes():
                if ttfb is None:
                    ttfb = time.perf_counter() - start_time
                # We continue to drain the stream to measure total time
                pass

            total_time = time.perf_counter() - start_time
            return {
                "url": base_url,
                "ttfb": f"{ttfb:.4f}s" if ttfb else "N/A",
                "total": f"{total_time:.4f}s",
                "status": response.status_code,
            }
    except httpx.TimeoutException:
        return {
            "url": base_url,
            "error": f"Timeout: No response within {TTFB_TIMEOUT_SECONDS}s",
        }
    except Exception as e:
        return {"url": base_url, "error": str(e)}


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

        # 2. Parse URL entry
        try:
            nar_path = next(
                line.split(": ")[1].strip()
                for line in resp.text.splitlines()
                if line.startswith("URL:")
            )
        except StopIteration:
            print("Could not find 'URL' entry in narinfo.")
            sys.exit(1)

        print(f"Testing NAR: {nar_path}\n")

        # 3. Call in parallel across all instances
        tasks = [fetch_metrics(client, url, nar_path) for url in target_urls]
        results = await asyncio.gather(*tasks)

        # 4. Report results
        url_width = max(len(r["url"]) for r in results)
        print(f"{'URL':<{url_width}} | {'TTFB':<10} | {'Total Time':<12} | {'Status'}")
        print("-" * (url_width + 40))
        for r in results:
            if "error" in r:
                print(f"{r['url']:<{url_width}} | ERROR: {r['error']}")
            else:
                print(
                    f"{r['url']:<{url_width}} | {r['ttfb']:<10} | {r['total']:<12} | {r['status']}"
                )


if __name__ == "__main__":
    asyncio.run(main())
