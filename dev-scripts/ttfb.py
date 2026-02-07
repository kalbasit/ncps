#!/usr/bin/env python3

import asyncio
import time
import httpx
import argparse
import sys

# Configuration constants
TARGET_PORTS = [8501, 8502, 8503]
TTFB_TIMEOUT_SECONDS = 10.0  # Adjust this value as needed

async def fetch_metrics(client, port, path):
    url = f"http://localhost:{port}/{path}"
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
                "port": port,
                "ttfb": f"{ttfb:.4f}s" if ttfb else "N/A",
                "total": f"{total_time:.4f}s",
                "status": response.status_code
            }
    except httpx.TimeoutException:
        return {"port": port, "error": f"Timeout: No response within {TTFB_TIMEOUT_SECONDS}s"}
    except Exception as e:
        return {"port": port, "error": str(e)}

async def main():
    parser = argparse.ArgumentParser(description="Measure NAR latency across ports.")
    parser.add_argument("hash", help="The Nix store hash (e.g., 9cmq42r...)")
    args = parser.parse_args()

    narinfo_url = f"http://localhost:{TARGET_PORTS[0]}/{args.hash}.narinfo"

    # Define the timeout structure. 'read' specifically limits the TTFB.
    timeout = httpx.Timeout(connect=5.0, read=TTFB_TIMEOUT_SECONDS, write=5.0, pool=5.0)

    async with httpx.AsyncClient(timeout=timeout) as client:
        # 1. Fetch narinfo
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

        # 3. Call in parallel
        tasks = [fetch_metrics(client, port, nar_path) for port in TARGET_PORTS]
        results = await asyncio.gather(*tasks)

        # 4. Report results
        print(f"{'Port':<8} | {'TTFB':<10} | {'Total Time':<12} | {'Status'}")
        print("-" * 55)
        for r in results:
            if "error" in r:
                print(f"{r['port']:<8} | ERROR: {r['error']}")
            else:
                print(f"{r['port']:<8} | {r['ttfb']:<10} | {r['total']:<12} | {r['status']}")

if __name__ == "__main__":
    asyncio.run(main())