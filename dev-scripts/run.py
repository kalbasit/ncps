#!/usr/bin/env python3

import argparse
import gzip
import http.client
import http.server
import json
import os
import shlex
import shutil
import signal
import subprocess
import sys
import threading
import time
from urllib.parse import urlparse

# --- Path Configuration ---
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# --- Configuration Constants ---
S3_CONFIG = {
    "bucket": "test-bucket",
    "endpoint": "http://127.0.0.1:9000",
    "region": "us-east-1",
    "access_key": "GK1234567890abcdef12345678",
    "secret_key": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
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
PROXY_PORT = 8500
PPROF_BASE_PORT = 7501

# Colors
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
RED = "\033[0;31m"
BLUE = "\033[0;34m"
NC = "\033[0m"

processes = []
extra_panes = []  # Track tmux panes created by this script
tmux_pids = []  # Track PIDs of processes running inside tmux panes
proxy_servers = []  # Track HA reverse-proxy servers started by this script


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

    # Stop the HA reverse proxy (if started) and release its port.
    for server in proxy_servers:
        try:
            server.shutdown()
            server.server_close()
        except OSError:
            pass  # Best-effort shutdown

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


# --- HA reverse proxy (dev convenience) ---------------------------------
#
# When running multiple instances (HA mode), front them with a single
# round-robin reverse proxy on PROXY_PORT so a Nix client can target one
# stable endpoint instead of per-instance ports. Implemented with the
# standard library only; request/response bodies are streamed with a bounded
# buffer so large NAR/narinfo transfers do not scale proxy memory with payload
# size.

PROXY_BUFFER = 64 * 1024

# Hop-by-hop headers must not be forwarded by a proxy (RFC 7230 §6.1).
HOP_BY_HOP_HEADERS = frozenset(
    {
        "connection",
        "keep-alive",
        "proxy-authenticate",
        "proxy-authorization",
        "proxy-connection",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
    }
)


def pick_backend(backends, counter):
    """Return the backend for a given request counter (round-robin)."""
    return backends[counter % len(backends)]


def _forwardable_request_headers(headers, backend):
    """Copy client request headers, dropping hop-by-hop ones and pinning Host
    to the selected backend."""
    out = []
    for key, value in headers.items():
        lowered = key.lower()
        if lowered in HOP_BY_HOP_HEADERS or lowered == "host":
            continue
        out.append((key, value))
    out.append(("Host", backend))
    return out


def make_proxy_handler(backends):
    """Build a BaseHTTPRequestHandler subclass that round-robins requests
    across `backends` (a list of "host:port" strings)."""
    state = {"counter": 0}
    lock = threading.Lock()

    class ProxyHandler(http.server.BaseHTTPRequestHandler):
        # Quiet: the instances do their own logging.
        def log_message(self, *_args):
            pass

        def _next_backend(self):
            with lock:
                counter = state["counter"]
                state["counter"] += 1
            return pick_backend(backends, counter)

        def _proxy(self):
            backend = self._next_backend()
            host, _, port = backend.partition(":")

            length_header = self.headers.get("Content-Length")
            try:
                conn = http.client.HTTPConnection(host, int(port), timeout=300)
                conn.putrequest(
                    self.command,
                    self.path,
                    skip_host=True,
                    skip_accept_encoding=True,
                )
                for key, value in _forwardable_request_headers(self.headers, backend):
                    conn.putheader(key, value)
                conn.endheaders()

                # Stream the request body (if any) without buffering it whole.
                if length_header is not None:
                    remaining = int(length_header)
                    while remaining > 0:
                        chunk = self.rfile.read(min(PROXY_BUFFER, remaining))
                        if not chunk:
                            break
                        conn.send(chunk)
                        remaining -= len(chunk)

                resp = conn.getresponse()
            except OSError as exc:
                # No health checks by design; a dead/slow backend yields a 502
                # without taking down the proxy thread.
                self.send_error(502, f"proxy backend error: {exc}")
                return

            # Forward the backend response faithfully (status + headers + body).
            self.send_response_only(resp.status, resp.reason)
            for key, value in resp.getheaders():
                if key.lower() in HOP_BY_HOP_HEADERS:
                    continue
                self.send_header(key, value)
            self.end_headers()
            shutil.copyfileobj(resp, self.wfile, PROXY_BUFFER)
            conn.close()

        do_GET = _proxy
        do_HEAD = _proxy
        do_PUT = _proxy
        do_POST = _proxy
        do_DELETE = _proxy

    return ProxyHandler


def start_proxy(host, port, backends):
    """Start a threaded round-robin reverse proxy and return the server.

    Raises OSError if the address cannot be bound (e.g. the port is in use)."""
    server = http.server.ThreadingHTTPServer(
        (host, port), make_proxy_handler(backends)
    )
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


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
    # Note: --log-level is consumed by the Python parser, so we must pass it explicitly to Go
    cmd = ["go", "run", ".", f"--log-level={args.log_level}"] + args.rest_args

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
        # Garage health check via its admin API. The admin port is fixed at 3903
        # by nix/process-compose/flake-module.nix (garageEnvironment).
        if (
            subprocess.call(
                ["curl", "-sf", "http://127.0.0.1:3903/health"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            != 0
        ):
            log(
                "❌ Garage is not running (Required for S3 or HA). Run 'nix run .#deps'",
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
    # Ensure directory exists for sqlite.
    # For postgres/mysql, ensure the database itself exists — `dbmate
    # up` used to do this implicitly, but `ncps migrate up` connects
    # to an existing database and won't create it. We use `dbmate
    # create` (idempotent-ish: errors if it already exists, which we
    # ignore) to keep the behavior compatible.
    if db_url.startswith("sqlite:"):
        path = db_url.replace("sqlite:", "")
        os.makedirs(os.path.dirname(path), exist_ok=True)
    else:
        if not shutil.which("dbmate"):
            log("'dbmate' is required for PostgreSQL/MySQL. Run inside the Nix dev shell.", RED)
            raise FileNotFoundError("dbmate not found in PATH")
        subprocess.run(
            ["dbmate", "--url", db_url, "create"],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

    # Use the embedded goose runner via `ncps migrate up`. This is the
    # only supported path on this branch (the migrations live in
    # migrations/<dialect>/, not the dbmate-default db/migrations/),
    # and it exercises the real dbmate→goose adoption logic if the
    # target database was initialized by an older ncps version.
    subprocess.run(
        ["go", "run", ".", "migrate", "up", f"--cache-database-url={db_url}"],
        cwd=REPO_ROOT,
        check=True,
    )


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

    # 3. Empty the S3 bucket's objects (keep the bucket itself).
    #
    # Uses boto3 — declared in the devshell (nix/devshells/flake-module.nix)
    # and already used by the k8s-tests — rather than the MinIO client `mc`,
    # which is NOT provided by the flake (it only happens to be on a
    # developer's global PATH) and whose long-term maintenance is uncertain.
    #
    # We delete objects rather than the bucket: the dev Garage access key is
    # scoped to the pre-provisioned `test-bucket` and lacks global
    # createBucket permission, so deleting + recreating the bucket fails
    # (Forbidden) and silently bricks S3 for the rest of the session.
    log("  Cleaning S3 storage...", YELLOW)
    try:
        import boto3
        from botocore.config import Config as BotoConfig

        bucket = S3_CONFIG["bucket"]
        s3 = boto3.client(
            "s3",
            endpoint_url=S3_CONFIG["endpoint"],
            aws_access_key_id=S3_CONFIG["access_key"],
            aws_secret_access_key=S3_CONFIG["secret_key"],
            region_name=S3_CONFIG["region"],
            config=BotoConfig(
                s3={"addressing_style": "path"},
                signature_version="s3v4",
                connect_timeout=5,
                read_timeout=15,
                retries={"max_attempts": 2},
            ),
        )
        paginator = s3.get_paginator("list_objects_v2")
        batch = []
        for page in paginator.paginate(Bucket=bucket):
            for obj in page.get("Contents", []):
                batch.append({"Key": obj["Key"]})
                if len(batch) == 1000:  # S3 delete_objects caps at 1000 keys
                    s3.delete_objects(Bucket=bucket, Delete={"Objects": batch})
                    batch = []
        if batch:
            s3.delete_objects(Bucket=bucket, Delete={"Objects": batch})
    except Exception as e:  # noqa: BLE001 — cleanup is best-effort
        log(f"  Warning: S3 cleanup failed: {e}", RED)

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
        "--enable-lazy-cdc",
        action="store_true",
        help="Enable the lazy CDC feature",
    )
    parser.add_argument(
        "--inflight-staging",
        action="store_true",
        help="Enable in-flight NAR staging (only engages with a distributed locker, e.g. --locker redis)",
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

    temp_dir = os.path.join(REPO_ROOT, "var/ncps/temp")
    os.makedirs(temp_dir, exist_ok=True)

    # Determine instance count
    num_instances = args.replicas

    # In-flight staging only engages with a distributed locker; the Go-side
    # InflightStagingEnabled() guard keeps it dormant otherwise. Reflect that
    # *effective* state in the banner and state.json so test drivers aren't
    # misled by a bare --inflight-staging on a local locker.
    staging_active = args.inflight_staging and args.locker == "redis"
    if not args.inflight_staging:
        staging_banner = "disabled"
    elif staging_active:
        staging_banner = "enabled"
    else:
        staging_banner = "dormant (requires redis locker)"

    log(f"\nStarting {num_instances} instance(s)...", BLUE)
    log(f"  Mode:    {'ha' if is_ha else 'single'}", BLUE)
    log(f"  DB:      {args.db}", BLUE)
    log(f"  Storage: {args.storage}", BLUE)
    log(f"  Locker:  {args.locker}", BLUE)
    log(f"  Inflight staging: {staging_banner}", BLUE)
    print("")

    use_tmux_split = is_ha and TmuxManager.is_in_tmux()
    if use_tmux_split:
        current_pane = TmuxManager.get_pane_id()

    instance_info = []

    for i in range(1, num_instances + 1):
        port = BASE_PORT + (i - 1)
        pprof_port = PPROF_BASE_PORT + (i - 1)

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
            f"--cache-temp-path={temp_dir}",
            f"--cache-database-url='{db_url}'",
            f"--server-addr=:{port}",
            f"--pprof-addr=:{pprof_port}",
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
        if args.enable_cdc or args.enable_lazy_cdc:
            cmd_app.append("--cache-cdc-enabled")
            cmd_app.append("--cache-cdc-min=16384")
            cmd_app.append("--cache-cdc-avg=65536")
            cmd_app.append("--cache-cdc-max=262144")
            cmd_app.append(
                f"--cache-cdc-lazy-chunking-enabled={'true' if args.enable_lazy_cdc else 'false'}"
            )
            if args.enable_lazy_cdc:
                cmd_app.append("--cache-cdc-lazy-recovery-schedule='@every 1m'")
                cmd_app.append("--cache-cdc-delete-delay=1m")
                cmd_app.append("--cache-cdc-lazy-cleanup-schedule='@every 1m'")
        if args.inflight_staging:
            # Only the enabled toggle is exposed; retention (5m) and part-size
            # (8 MiB) keep their Go defaults. The feature self-disables unless the
            # locker is distributed (see InflightStagingEnabled() guard).
            cmd_app.append("--cache-inflight-staging-enabled")
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

    # In HA mode, front the instances with a single round-robin reverse proxy
    # so a Nix client can target one stable endpoint (PROXY_PORT) instead of
    # juggling per-instance ports. Single-instance runs skip this.
    proxy_endpoint = None
    if is_ha:
        backends = [f"127.0.0.1:{inst['port']}" for inst in instance_info]
        try:
            server = start_proxy("127.0.0.1", PROXY_PORT, backends)
        except OSError as exc:
            log(
                f"⛔ ERROR: failed to bind HA proxy on port {PROXY_PORT}: {exc}",
                RED,
            )
            # Tear down the instances we just started before bailing out.
            for p in processes:
                if p.poll() is None:
                    p.terminate()
            for pane_id in extra_panes:
                try:
                    TmuxManager.kill_pane(pane_id)
                except subprocess.CalledProcessError:
                    pass
            for pid in tmux_pids:
                _kill_tree(pid, signal.SIGTERM)
            sys.exit(1)
        proxy_servers.append(server)
        proxy_endpoint = {"host": "127.0.0.1", "port": PROXY_PORT}
        log(
            f"HA proxy listening on http://127.0.0.1:{PROXY_PORT} → {', '.join(backends)}",
            GREEN,
        )

    state_config = {
        "cdc": args.enable_cdc,
        "inflight_staging": staging_active,
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
    if proxy_endpoint:
        state_config["proxy"] = proxy_endpoint

    state_path = write_state_file(instance_info, state_config)
    log(f"State written to {state_path}", BLUE)

    # Wait for interrupts
    signal.signal(signal.SIGINT, cleanup)
    signal.signal(signal.SIGTERM, cleanup)

    # Keep main thread alive
    signal.pause()


if __name__ == "__main__":
    main()
