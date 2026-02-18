#!/usr/bin/env python3

import argparse
import gzip
import json
import os
import shlex
import shutil
import signal
import subprocess
import sys
import time
from urllib.parse import urlparse

# --- Path Configuration ---
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

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
    "postgres": os.environ.get(
        "NCPS_DEV_POSTGRES_URL",
        "postgresql://dev-user:dev-password@127.0.0.1:5432/dev-db?sslmode=disable",
    ),
    "mysql": os.environ.get(
        "NCPS_DEV_MYSQL_URL", "mysql://dev-user:dev-password@127.0.0.1:3306/dev-db"
    ),
    "sqlite": f"sqlite:{os.path.join(REPO_ROOT, 'var/ncps/db/db.sqlite')}",
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
extra_panes = []  # Track tmux panes created by this script
tmux_pids = []  # Track PIDs of processes running inside tmux panes


class TmuxManager:
    @staticmethod
    def is_in_tmux():
        return "TMUX" in os.environ

    @staticmethod
    def get_pane_id():
        return subprocess.check_output(
            ["tmux", "display-message", "-p", "#{pane_id}"], text=True
        ).strip()

    @staticmethod
    def split_window(target_pane, command=None):
        # Split vertically (even layout will be applied later)
        args = [
            "tmux",
            "split-window",
            "-d",
            "-t",
            target_pane,
            "-P",
            "-F",
            "#{pane_id}",
        ]
        if command:
            args.append(command)
        return subprocess.check_output(args, text=True).strip()

    @staticmethod
    def select_layout(layout):
        subprocess.run(["tmux", "select-layout", layout], check=True)

    @staticmethod
    def get_pane_pid(pane_id):
        return int(
            subprocess.check_output(
                ["tmux", "display-message", "-t", pane_id, "-p", "#{pane_pid}"],
                text=True,
            ).strip()
        )

    @staticmethod
    def kill_pane(pane_id):
        subprocess.run(["tmux", "kill-pane", "-t", pane_id], check=True)


def log(msg, color=NC):
    print(f"{color}{msg}{NC}")


def cleanup(signum, frame):
    log("\nShutting down instances...", YELLOW)
    for p in processes:
        if p.poll() is None:
            p.terminate()

    # Poll until all processes exit (up to 10 seconds)
    deadline = time.time() + 10
    while time.time() < deadline:
        if all(p.poll() is not None for p in processes):
            break
        time.sleep(0.25)

    for p in processes:
        if p.poll() is None:
            log(f"Force killing process {p.pid}", RED)
            p.kill()

    # Kill extra tmux panes and their processes
    for pane_id in extra_panes:
        try:
            TmuxManager.kill_pane(pane_id)
        except subprocess.CalledProcessError:
            pass  # Pane might already be gone

    # Terminate tmux-managed process trees by PID
    for pid in tmux_pids:
        _kill_tree(pid, signal.SIGTERM)

    # Poll until tmux-managed processes exit (up to 10 seconds)
    if tmux_pids:
        deadline = time.time() + 10
        while time.time() < deadline:
            alive = [pid for pid in tmux_pids if _pid_exists(pid)]
            if not alive:
                break
            time.sleep(0.25)

        # Force-kill any remaining tmux-managed process trees
        for pid in tmux_pids:
            _kill_tree(pid, signal.SIGKILL)

    remove_state_file()
    log("All instances stopped.", GREEN)
    sys.exit(0)


def rotate_logs(log_path, max_backups=5):
    """
    Rotates the log file at log_path.
    Existing log is moved to log_path.1.gz, log_path.1.gz to log_path.2.gz, etc.
    """
    if not os.path.exists(log_path):
        return

    # Rotate existing backups
    for i in range(max_backups - 1, 0, -1):
        s = f"{log_path}.{i}.gz"
        d = f"{log_path}.{i + 1}.gz"
        if os.path.exists(s):
            os.rename(s, d)

    # Rotate current log
    if os.path.exists(log_path):
        dest = f"{log_path}.1.gz"
        with open(log_path, "rb") as f_in:
            with gzip.open(dest, "wb") as f_out:
                shutil.copyfileobj(f_in, f_out)
        os.remove(log_path)


def _pid_exists(pid):
    """Return True if a process with the given PID is still running."""
    try:
        os.kill(pid, 0)
        return True
    except (ProcessLookupError, OSError):
        return False


def _kill_tree(pid, sig):
    """Send sig to pid and all its descendants (depth-first, children first)."""
    # Find children via pgrep -P
    try:
        children = subprocess.check_output(["pgrep", "-P", str(pid)], text=True).split()
        for child in children:
            _kill_tree(int(child), sig)
    except subprocess.CalledProcessError:
        pass  # No children or pgrep failed
    try:
        os.kill(pid, sig)
    except (ProcessLookupError, OSError):
        pass


def write_state_file(instances, config):
    """Write state file with port and optional pid info for running instances."""
    state_dir = os.path.join(REPO_ROOT, "var/ncps")
    os.makedirs(state_dir, exist_ok=True)
    state_path = os.path.join(state_dir, "state.json")
    data = {**config, "instances": instances}
    with open(state_path, "w") as f:
        json.dump(data, f, indent=2)
    return state_path


def remove_state_file():
    """Remove state file on clean exit."""
    state_path = os.path.join(REPO_ROOT, "var/ncps/state.json")
    try:
        os.remove(state_path)
    except FileNotFoundError:
        pass


def internal_start_instance(args):
    """
    Internal function to run the actual process and pipe output to log file.
    This is what watchexec calls.
    """
    log_path = args.log_file
    rotate_logs(log_path)

    # Reconstruct the command to run the actual app
    # We stripped the wrapper args, now run 'go run .' with the rest
    cmd = ["go", "run", "."] + args.rest_args

    # Ensure log directory exists
    os.makedirs(os.path.dirname(log_path), exist_ok=True)

    log(f"Starting instance, logging to {log_path}", GREEN)

    # Use line buffering and text mode for real-time output processing
    p = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        cwd=os.getcwd(),
        bufsize=1,
        universal_newlines=True,
    )

    def handler(signum, frame):
        p.terminate()
        try:
            p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            p.kill()
        sys.exit(0)

    signal.signal(signal.SIGINT, handler)
    signal.signal(signal.SIGTERM, handler)

    with open(log_path, "w") as f_log:
        while True:
            line = p.stdout.readline()
            if not line and p.poll() is not None:
                break
            if line:
                f_log.write(line)
                f_log.flush()
                if args.log_to_stdout:
                    sys.stdout.write(line)
                    sys.stdout.flush()

    sys.exit(p.poll())


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
    if args.storage == "s3" or (args.replicas > 1):
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


def perform_clean():
    log("Cleaning previous data...", YELLOW)

    # 1. Remove var/ncps folder
    ncps_var_dir = os.path.join(REPO_ROOT, "var/ncps")
    if os.path.exists(ncps_var_dir):
        log(f"  Removing {ncps_var_dir}", YELLOW)
        shutil.rmtree(ncps_var_dir, ignore_errors=True)

    # 2. Remove tables in Postgres/MySQL
    for engine in ["postgres", "mysql"]:
        url = DB_CONFIG[engine]
        if shutil.which("dbmate"):
            log(f"  Cleaning {engine} database...", YELLOW)
            try:
                subprocess.run(
                    ["dbmate", "--url", url, "drop"],
                    check=False,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    timeout=5,
                )
            except subprocess.TimeoutExpired:
                log(f"  Warning: {engine} cleanup timed out", RED)

    # 3. Remove all s3 files
    if shutil.which("mc"):
        log("  Cleaning S3 storage...", YELLOW)
        endpoint = S3_CONFIG["endpoint"]
        access_key = S3_CONFIG["access_key"]
        secret_key = S3_CONFIG["secret_key"]
        bucket = S3_CONFIG["bucket"]
        mc_env = os.environ.copy()
        # Construct MC_HOST_clean env var. Note: we use a specific alias 'clean'
        # The format for MC_HOST_<alias> is http(s)://ACCESS_KEY:SECRET_KEY@HOST:PORT
        parsed_endpoint = urlparse(endpoint)
        mc_url = parsed_endpoint._replace(netloc=f"{access_key}:{secret_key}@{parsed_endpoint.hostname}:{parsed_endpoint.port}").geturl()
        mc_env["MC_HOST_clean"] = mc_url
        try:
            subprocess.run(
                ["mc", "rb", "--force", f"clean/{bucket}"],
                env=mc_env,
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=5,
            )
            # Re-create the bucket
            subprocess.run(
                ["mc", "mb", f"clean/{bucket}"],
                env=mc_env,
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=5,
            )
        except subprocess.TimeoutExpired:
            log("  Warning: S3 cleanup timed out", RED)

    # 4. Delete all redis keys
    if shutil.which("redis-cli"):
        log("  Cleaning Redis keys...", YELLOW)
        r_host, r_port = REDIS_ADDR.split(":")
        try:
            subprocess.run(
                ["redis-cli", "-h", r_host, "-p", r_port, "flushall"],
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                timeout=5,
            )
        except subprocess.TimeoutExpired:
            log("  Warning: Redis cleanup timed out", RED)

    log("Cleanup complete.\n", GREEN)


def main():
    parser = argparse.ArgumentParser(
        description="Run ncps instances with configurable backends."
    )
    parser.add_argument(
        "--clean",
        action="store_true",
        help="Clean all previous data before starting (removes var/ncps, drops DB tables, clears S3, flushes Redis)",
    )
    parser.add_argument(
        "--enable-cdc",
        action="store_true",
        help="Enable the CDC feature",
    )
    parser.add_argument(
        "--log-level",
        choices=["debug", "info", "warn", "error", "fatal", "panic"],
        default="debug",
        help="Set the log level",
    )
    parser.add_argument(
        "--replicas",
        type=int,
        default=1,
        help="Number of instances to run (default: 1, use >1 for HA mode)",
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

    parser.add_argument(
        "--internal-start-instance",
        action="store_true",
        help=argparse.SUPPRESS,  # Hidden flag for internal wrapper use
    )
    parser.add_argument(
        "--log-file",
        help=argparse.SUPPRESS,  # Hidden flag for internal wrapper use
    )
    parser.add_argument(
        "--log-to-stdout",
        action="store_true",
        help="Also print logs to stdout (in addition to log file)",
    )

    # We use parse_known_args because when running internally, we have
    # a bunch of flags for the Go app that we don't define here.
    if "--internal-start-instance" in sys.argv:
        # Initial parse to check for the internal flag
        args, rest = parser.parse_known_args()
        args.rest_args = rest
        internal_start_instance(args)
        return

    args = parser.parse_args()

    if args.clean:
        perform_clean()

    # --- Guard Rails ---
    if args.replicas < 1:
        log("⛔ ERROR: --replicas must be at least 1.", RED)
        sys.exit(1)

    is_ha = args.replicas > 1

    if is_ha:
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
    local_storage_path = os.path.join(REPO_ROOT, "var/ncps/storage")
    os.makedirs(local_storage_path, exist_ok=True)

    # Determine instance count
    num_instances = args.replicas

    log(f"\nStarting {num_instances} instance(s)...", BLUE)
    log(f"  Mode:    {'ha' if is_ha else 'single'}", BLUE)
    log(f"  DB:      {args.db}", BLUE)
    log(f"  Storage: {args.storage}", BLUE)
    log(f"  Locker:  {args.locker}", BLUE)
    print("")

    use_tmux_split = is_ha and TmuxManager.is_in_tmux()
    if use_tmux_split:
        current_pane = TmuxManager.get_pane_id()

    instance_info = []

    for i in range(1, num_instances + 1):
        port = BASE_PORT + (i - 1)

        # Instead of calling 'go run .' directly, we call ourselves with the internal flag
        # This wrapper handles log rotation and redirection.
        # We need the absolute path to this script and the executables to be safe.
        script_path = os.path.abspath(__file__)
        log_file = os.path.join(REPO_ROOT, f"var/log/ncps-{port}.log")
        watchexec_path = shutil.which("watchexec") or "watchexec"
        direnv_path = shutil.which("direnv")
        if not direnv_path:
            log(
                "❌ 'direnv' is not installed or not in your PATH. It is required to run instances.",
                RED,
            )
            sys.exit(1)

        # Chunk 1: Watchexec arguments
        cmd_watchexec = [
            watchexec_path,
            "-e",
            "go",
            "-c",
            "clear",
            "-r",
            "--",
        ]

        # Chunk 2: Python wrapper arguments (wrapped in direnv exec for correct env)
        cmd_wrapper = [
            direnv_path,
            "exec",
            REPO_ROOT,
            "python3",
            script_path,
            "--internal-start-instance",
            f"--log-file={log_file}",
        ]

        if args.log_to_stdout:
            cmd_wrapper.append("--log-to-stdout")

        # Chunk 3: Go application arguments
        cmd_app = [
            "--analytics-reporting-enabled=false",
            f"--log-level={args.log_level}",
            "serve",
            "--cache-allow-put-verb",
            "--cache-hostname=cache.example.com",
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
            cmd_app.append(f"--cache-upstream-url='{url}'")

        keys = args.cache_public_key or [
            "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
        ]
        for key in keys:
            cmd_app.append(f"--cache-upstream-public-key={key}")

        if args.analytics_reporting_samples:
            cmd_app.append("--analytics-reporting-samples")

        # Storage Args
        if args.enable_cdc:
            cmd_app.append("--cache-cdc-enabled")
        if args.storage == "local":
            cmd_app.extend(["--cache-storage-local", local_storage_path])
        else:
            cmd_app.extend(
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
            cmd_app.extend(
                [
                    "--cache-lock-backend=redis",
                    f"--cache-redis-addrs={REDIS_ADDR}",
                    f"--cache-lock-download-ttl=5m",
                    f"--cache-lock-lru-ttl=30m",
                ]
            )

        # Combine all parts
        cmd = cmd_watchexec + cmd_wrapper + cmd_app

        # Start Process
        log(f"Starting Instance {i} on port {port}...", GREEN)

        if use_tmux_split:
            # Construct shell command
            cmd_str = shlex.join(cmd)
            # Use atomic split-and-run to avoid timing issues with send-keys
            new_pane = TmuxManager.split_window(current_pane, command=cmd_str)
            extra_panes.append(new_pane)
            TmuxManager.select_layout("tiled")
            pane_pid = TmuxManager.get_pane_pid(new_pane)
            tmux_pids.append(pane_pid)
            instance_info.append({"port": port, "pid": pane_pid})
        else:
            # Run locally (Instance 1 or single mode)
            p = subprocess.Popen(cmd)
            processes.append(p)
            instance_info.append({"port": port, "pid": p.pid})

    state_config = {
        "cdc": args.enable_cdc,
        "db": args.db,
        "db_url": db_url,
        "storage": args.storage,
        "storage_path": local_storage_path,
        "locker": args.locker,
    }
    if args.storage == "s3":
        state_config["s3"] = {
            "bucket": S3_CONFIG["bucket"],
            "endpoint": S3_CONFIG["endpoint"],
            "region": S3_CONFIG["region"],
            "access_key": S3_CONFIG["access_key"],
            "secret_key": S3_CONFIG["secret_key"],
        }

    state_path = write_state_file(instance_info, state_config)
    log(f"State written to {state_path}", BLUE)

    # Wait for interrupts
    signal.signal(signal.SIGINT, cleanup)
    signal.signal(signal.SIGTERM, cleanup)

    # Keep main thread alive
    signal.pause()


if __name__ == "__main__":
    main()
