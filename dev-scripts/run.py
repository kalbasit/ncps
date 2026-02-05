#!/usr/bin/env python3

import argparse
import os
import shlex
import signal
import subprocess
import sys
import time
from urllib.parse import urlparse

# --- Configuration Constants ---
S3_CONFIG = {
    "bucket": "test-bucket",
    "endpoint": "http://127.0.0.1:9000",
    "region": "us-east-1",
    "access_key": "test-access-key",
    "secret_key": "test-secret-key",
}

# Database URLs matching bash scripts
DB_CONFIG = {
    "postgres": "postgresql://dev-user:dev-password@127.0.0.1:5432/dev-db?sslmode=disable",
    "mysql": "mysql://dev-user:dev-password@127.0.0.1:3306/dev-db",
    "sqlite": "sqlite:var/ncps/db/db.sqlite",
}

REDIS_ADDR = "127.0.0.1:6379"
BASE_PORT = 8501

# Colors
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
RED = "\033[0;31m"
BLUE = "\033[0;34m"
NC = "\033[0m"

processes = []


def log(msg, color=NC):
    print(f"{color}{msg}{NC}")


def cleanup(signum, frame):
    log("\nShutting down instances...", YELLOW)
    for p in processes:
        if p.poll() is None:
            try:
                # Kill the entire process group (watchexec, sed, etc.)
                os.killpg(os.getpgid(p.pid), signal.SIGTERM)
            except ProcessLookupError:
                # Process already exited
                pass

    # Wait for graceful exit
    time.sleep(1)

    for p in processes:
        if p.poll() is None:
            log(f"Force killing process group {p.pid}", RED)
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGKILL)
            except ProcessLookupError:
                # Process already exited
                pass

    log("All instances stopped.", GREEN)
    sys.exit(0)


def check_dependencies(args):
    """Simple dependency checks using subprocess."""
    deps_ok = True

    # Check DB
    if args.db == "postgres":
        db_url = DB_CONFIG["postgres"]
        parsed = urlparse(db_url)
        if (
            subprocess.call(
                [
                    "pg_isready",
                    "-h",
                    parsed.hostname,
                    "-p",
                    str(parsed.port),
                    "-U",
                    parsed.username,
                ],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            != 0
        ):
            log("❌ PostgreSQL is not running. Run 'nix run .#deps'", RED)
            deps_ok = False
    elif args.db == "mysql":
        db_url = DB_CONFIG["mysql"]
        parsed = urlparse(db_url)
        if (
            subprocess.call(
                [
                    "mysqladmin",
                    "-h",
                    parsed.hostname,
                    "-P",
                    str(parsed.port),
                    "-u",
                    parsed.username,
                    f"--password={parsed.password}",
                    "ping",
                ],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            != 0
        ):
            log("❌ MySQL is not running. Run 'nix run .#deps'", RED)
            deps_ok = False

    # Check Storage/Locker
    if args.storage == "s3" or args.mode == "ha":
        # MinIO check
        # Note: We could parse S3_CONFIG['endpoint'] here if we wanted to be strictly dynamic,
        # but the health path (/minio/health/live) is specific to MinIO anyway.
        if (
            subprocess.call(
                ["curl", "-s", f"{S3_CONFIG['endpoint']}/minio/health/live"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            != 0
        ):
            log(
                "❌ MinIO is not running (Required for S3 or HA). Run 'nix run .#deps'",
                RED,
            )
            deps_ok = False

    if args.locker == "redis":
        # Parse REDIS_ADDR assuming format host:port
        r_host, r_port = REDIS_ADDR.split(":")
        if (
            subprocess.call(
                ["redis-cli", "-h", r_host, "-p", r_port, "ping"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            != 0
        ):
            log("❌ Redis is not running. Run 'nix run .#deps'", RED)
            deps_ok = False

    if not deps_ok:
        sys.exit(1)


def run_db_migration(db_url):
    log(f"Migrating database: {db_url}", YELLOW)
    # Ensure directory exists for sqlite
    if db_url.startswith("sqlite:"):
        path = db_url.replace("sqlite:", "")
        os.makedirs(os.path.dirname(path), exist_ok=True)

    subprocess.run(["dbmate", "--no-dump-schema", "--url", db_url, "up"], check=True)


def main():
    parser = argparse.ArgumentParser(
        description="Run ncps instances with configurable backends."
    )
    parser.add_argument(
        "--enable-cdc",
        action="store_true",
        help="Enable the CDC feature",
    )
    parser.add_argument(
        "--mode",
        choices=["single", "ha"],
        default="single",
        help="Run mode: single instance or High Availability",
    )
    parser.add_argument(
        "--db",
        choices=["sqlite", "postgres", "mysql"],
        default="sqlite",
        help="Database backend",
    )
    parser.add_argument(
        "--storage", choices=["local", "s3"], default="local", help="Storage backend"
    )
    parser.add_argument(
        "--locker",
        choices=["local", "redis"],
        default="local",
        help="Locking mechanism",
    )
    parser.add_argument(
        "--instances",
        type=int,
        default=3,
        help="Number of instances for HA mode (default: 3)",
    )
    parser.add_argument(
        "--analytics-reporting-samples",
        action="store_true",
        help="Enable printing analytics samples to stdout",
    )
    parser.add_argument(
        "--cache-url",
        action="append",
        help="URL for the cache backend (can be specified multiple times)",
    )
    parser.add_argument(
        "--cache-public-key",
        action="append",
        help="Public key for cache validation (can be specified multiple times)",
    )

    args = parser.parse_args()

    # --- Guard Rails ---
    if args.mode == "ha":
        if args.locker == "local":
            log(
                "⛔ ERROR: HA mode requires a distributed locker. You cannot use '--locker local'. Switch to '--locker redis'.",
                RED,
            )
            sys.exit(1)

        # While HA *can* work with local storage if it's a shared path,
        # for simplicity and safety relative to the user prompt:
        if args.storage == "local":
            log(
                "⚠️  WARNING: Running HA with local storage. Ensure all instances can access the shared path.",
                YELLOW,
            )
            # We will enforce a shared directory below instead of mktemp

        if args.db == "sqlite":
            log(
                "⛔ ERROR: Running HA with SQLite is not supported. Switch to 'postgres' or 'mysql'.",
                RED,
            )
            sys.exit(1)

    # Validate deps
    check_dependencies(args)

    # Database URL
    db_url = DB_CONFIG[args.db]

    # Force absolute path for sqlite.
    if args.db == "sqlite":
        # Split 'sqlite:' from the path, resolve absolute path, and recombine
        # This ensures dbmate and the Go app see the exact same file regardless of CWD changes
        prefix, relative_path = db_url.split(":", 1)
        abs_path = os.path.abspath(relative_path)
        db_url = f"{prefix}:{abs_path}"
        log(f"Resolved absolute SQLite path: {abs_path}", BLUE)

    # Run Migration
    try:
        run_db_migration(db_url)
    except subprocess.CalledProcessError:
        log("❌ Migration failed.", RED)
        sys.exit(1)

    # Define base arguments
    # Note: Using a fixed path for local storage in python to allow shared local storage
    # instead of 'mktemp' which isolates instances.
    local_storage_path = os.path.abspath("var/ncps/storage")
    os.makedirs(local_storage_path, exist_ok=True)

    # Determine instance count
    num_instances = 1 if args.mode == "single" else args.instances

    log(f"\nStarting {num_instances} instance(s)...", BLUE)
    log(f"  Mode:    {args.mode}", BLUE)
    log(f"  DB:      {args.db}", BLUE)
    log(f"  Storage: {args.storage}", BLUE)
    log(f"  Locker:  {args.locker}", BLUE)
    print("")

    for i in range(1, num_instances + 1):
        port = BASE_PORT + (i - 1)
        prefix = f"[localhost:{port}]"

        cmd = [
            "watchexec",
            "-e",
            "go",
            "-c",
            "clear",
            "-r",
            "go",
            "run",
            ".",
            "--analytics-reporting-enabled=false",
            "serve",
            "--cache-allow-put-verb",
            f"--cache-hostname=cache-{i}.example.com",
            f"--cache-database-url='{db_url}'",
            f"--server-addr=:{port}",
        ]

        if bool(args.cache_url) != bool(args.cache_public_key):
            log(
                "⚠️  WARNING: --cache-url and --cache-public-key should be provided together. Using defaults for the missing one may lead to errors.",
                YELLOW,
            )

        urls = args.cache_url or ["https://cache.nixos.org"]
        for url in urls:
            cmd.append(f"--cache-upstream-url={url}")

        keys = args.cache_public_key or [
            "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
        ]
        for key in keys:
            cmd.append(f"--cache-upstream-public-key={key}")

        if args.analytics_reporting_samples:
            cmd.append("--analytics-reporting-samples")

        # Storage Args
        if args.enable_cdc:
            cmd.append("--cache-cdc-enabled")
        if args.storage == "local":
            cmd.extend(["--cache-storage-local", local_storage_path])
        else:
            cmd.extend(
                [
                    f"--cache-storage-s3-bucket={S3_CONFIG['bucket']}",
                    f"--cache-storage-s3-endpoint={S3_CONFIG['endpoint']}",
                    f"--cache-storage-s3-region={S3_CONFIG['region']}",
                    f"--cache-storage-s3-access-key-id={S3_CONFIG['access_key']}",
                    f"--cache-storage-s3-secret-access-key={S3_CONFIG['secret_key']}",
                    "--cache-storage-s3-force-path-style",
                ]
            )

        # Locker Args
        if args.locker == "redis":
            cmd.extend(
                [
                    "--cache-lock-backend=redis",
                    f"--cache-redis-addrs={REDIS_ADDR}",
                    "--cache-lock-download-ttl=5m",
                    "--cache-lock-lru-ttl=30m",
                ]
            )

        # Quote each element so the shell treats them as literal strings
        quoted_cmd = [shlex.quote(arg) for arg in cmd]

        # Join the command into a string and add the sed pipe
        # 2>&1 redirects stderr to stdout so both are prefixed
        cmd_str = " ".join(quoted_cmd)
        full_command = f"{cmd_str} 2>&1 | sed 's,^,{prefix.replace('\\', '\\\\')} ,/'"

        # Start Process
        log(f"Starting Instance {i} on port {port}...", GREEN)

        # Use a shell pipe to prefix output, similar to the bash script
        # Note: We are not piping through sed in python to keep signal handling simple,
        # but you could add a pipe handler if strictly required.
        # Standard Popen here for reliability.
        p = subprocess.Popen(full_command, shell=True, preexec_fn=os.setsid)
        processes.append(p)

        # Stagger start for HA
        if num_instances > 1:
            time.sleep(1)

    # Wait for interrupts
    signal.signal(signal.SIGINT, cleanup)
    signal.signal(signal.SIGTERM, cleanup)

    # Keep main thread alive
    signal.pause()


if __name__ == "__main__":
    main()
