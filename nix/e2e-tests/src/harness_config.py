"""Shared constants and small helpers for the e2e harness.

These mirror ``dev-scripts/run.py``'s ``DB_CONFIG`` / ``S3_CONFIG`` and the
former drivers' constants so the harness talks to the same fixed-port dev
backends. Unlike the old drivers (which lived in ``dev-scripts/`` and derived
the repo root from ``__file__``), this code runs from the Nix store, so the
repo root is resolved from the invocation working directory.
"""

from __future__ import annotations

import os
import sys

# Fixed dev ports (from `nix run .#deps`); run.py assigns replica HTTP ports as
# BASE_PORT + (i - 1).
BASE_PORT = 8501
S3_PORT = 9000
POSTGRES_PORT = 5432
MYSQL_PORT = 3306
REDIS_PORT = 6379


def find_repo_root() -> str:
    """Locate the ncps repo root by walking up from the working directory.

    The harness binary lives in the Nix store, so it cannot use ``__file__``;
    local mode drives ``dev-scripts/run.py`` and ``go run .`` which must run in
    the real repo. ``NCPS_REPO_ROOT`` overrides the search.
    """
    explicit = os.environ.get("NCPS_REPO_ROOT")
    if explicit:
        return os.path.abspath(explicit)
    cur = os.getcwd()
    while True:
        if os.path.exists(os.path.join(cur, "flake.nix")) and os.path.exists(
            os.path.join(cur, "dev-scripts", "run.py")
        ):
            return cur
        parent = os.path.dirname(cur)
        if parent == cur:
            # Fall back to cwd; local mode will fail loudly if run.py is absent.
            return os.getcwd()
        cur = parent


REPO_ROOT = find_repo_root()
VAR_NCPS = os.path.join(REPO_ROOT, "var", "ncps")
RESULTS_ROOT = os.path.join(REPO_ROOT, ".e2e-results", "harness")
STATE_FILE = os.path.join(VAR_NCPS, "state.json")
LOG_DIR = os.path.join(REPO_ROOT, "var", "log")
LOCAL_STORAGE_PATH = os.path.join(REPO_ROOT, "var", "ncps", "storage")
TEMP_PATH = os.path.join(REPO_ROOT, "var", "ncps", "temp")

RUN_PY = os.path.join(REPO_ROOT, "dev-scripts", "run.py")
PYTHON = sys.executable

# Database URLs mirror dev-scripts/run.py's DB_CONFIG (run.py keyword: postgres/mysql/sqlite).
DB_URLS = {
    "sqlite": f"sqlite:{os.path.join(REPO_ROOT, 'var/ncps/db/db.sqlite')}",
    "postgres": os.environ.get(
        "NCPS_DEV_POSTGRES_URL",
        "postgresql://dev-user:dev-password@127.0.0.1:5432/dev-db?sslmode=disable",
    ),
    "mysql": os.environ.get(
        "NCPS_DEV_MYSQL_URL",
        "mysql://dev-user:dev-password@127.0.0.1:3306/dev-db",
    ),
}

# S3 flags mirror run.py's S3_CONFIG so CLI subcommands hit the same bucket.
S3_CONFIG = {
    "bucket": "test-bucket",
    "endpoint": f"http://127.0.0.1:{S3_PORT}",
    "region": "us-east-1",
    "access_key": "GK1234567890abcdef12345678",
    "secret_key": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
}

# CDC config keys (pkg/config/config.go).
CDC_KEYS = ["cdc_enabled", "cdc_min", "cdc_avg", "cdc_max"]

# ANSI colors.
G = "\033[0;32m"
Y = "\033[1;33m"
R = "\033[0;31m"
B = "\033[0;34m"
N = "\033[0m"


def log(msg: str, c: str = N) -> None:
    print(f"{c}{msg}{N}", flush=True)


def section(msg: str) -> None:
    bar = "=" * 78
    log(f"\n{bar}\n{msg}\n{bar}", B)


class AssertionFailure(Exception):
    """A phase invariant did not hold."""


def check(cond: bool, msg: str) -> None:
    if not cond:
        raise AssertionFailure(msg)
    log(f"  ✓ {msg}", G)


def storage_flags(storage: str):
    """ncps CLI storage flags for `local` or `s3`."""
    if storage == "local":
        return ["--cache-storage-local", LOCAL_STORAGE_PATH]
    return [
        f"--cache-storage-s3-bucket={S3_CONFIG['bucket']}",
        f"--cache-storage-s3-endpoint={S3_CONFIG['endpoint']}",
        f"--cache-storage-s3-region={S3_CONFIG['region']}",
        f"--cache-storage-s3-access-key-id={S3_CONFIG['access_key']}",
        f"--cache-storage-s3-secret-access-key={S3_CONFIG['secret_key']}",
        "--cache-storage-s3-force-path-style",
    ]
