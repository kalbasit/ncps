#!/usr/bin/env python3

import atexit
import os
import shutil
import subprocess
import sys
import tempfile
from urllib.parse import urlparse

# Colors for output
RED = "\033[0;31m"
GREEN = "\033[0;32m"
YELLOW = "\033[1;33m"
BLUE = "\033[0;34m"
NC = "\033[0m"  # No Color

# Database URLs
POSTGRES_URL = (
    "postgresql://dev-user:dev-password@127.0.0.1:5432/dev-db?sslmode=disable"
)
MYSQL_URL = "mysql://dev-user:dev-password@127.0.0.1:3306/dev-db"

# Handle SQLite temp directory safely
SQLITE_DIR = tempfile.mkdtemp()
SQLITE_URL = f"sqlite:{SQLITE_DIR}/db.sqlite"


def cleanup():
    """Equivalent to the 'trap' in bash."""
    if os.path.exists(SQLITE_DIR):
        shutil.rmtree(SQLITE_DIR)


atexit.register(cleanup)


def log(msg, color=NC):
    print(f"{color}{msg}{NC}")


def check_command(cmd_list, name):
    """
    Checks connectivity using the provided command list.
    Returns True if successful, False otherwise.
    """
    try:
        # Capture output to suppress it unless debugging is needed
        subprocess.run(
            cmd_list, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
        )
        log(f"✓ {name} is ready", GREEN)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        log(f"ERROR: {name} is not running or accessible", RED)
        log(f"Skipping migration for {name}...", YELLOW)
        return False


def run_dbmate(url):
    """Runs dbmate up for the given URL."""
    try:
        subprocess.run(["dbmate", "--url", url, "up"], check=True)
        return True
    except subprocess.CalledProcessError:
        log("Migration failed", RED)
        sys.exit(1)


def migrate_postgres():
    log("1. Migrating PostgreSQL...", YELLOW)

    # Parse URL for credentials
    parsed = urlparse(POSTGRES_URL)
    # pg_isready -h host -p port -U user
    cmd = [
        "pg_isready",
        "-h",
        parsed.hostname,
        "-p",
        str(parsed.port),
        "-U",
        parsed.username,
    ]

    if check_command(cmd, "PostgreSQL"):
        run_dbmate(POSTGRES_URL)
        log("PostgreSQL migration complete.", GREEN)
    else:
        log("Skipped PostgreSQL.", RED)
    print()


def migrate_mysql():
    log("2. Migrating MySQL...", YELLOW)

    # Parse URL for credentials
    parsed = urlparse(MYSQL_URL)
    # mysqladmin -h host -P port -u user --password=pass ping
    cmd = [
        "mysqladmin",
        "-h",
        parsed.hostname,
        "-P",
        str(parsed.port),
        "-u",
        parsed.username,
        f"--password={parsed.password}",
        "ping",
    ]

    if check_command(cmd, "MySQL"):
        run_dbmate(MYSQL_URL)
        log("MySQL migration complete.", GREEN)
    else:
        log("Skipped MySQL.", RED)
    print()


def migrate_sqlite():
    log("3. Migrating SQLite...", YELLOW)

    # Ensure directory exists (created by mkdtemp, but logic mirrors bash)
    if os.path.isdir(SQLITE_DIR):
        log("✓ SQLite directory ready", GREEN)
        run_dbmate(SQLITE_URL)
        log("SQLite migration complete.", GREEN)
    else:
        log("SQLite directory error", RED)
    print()


def main():
    log("Starting migrations for all detected database configurations...", BLUE)
    print()

    migrate_postgres()
    migrate_mysql()
    migrate_sqlite()

    log("All requested migrations finished.", GREEN)


if __name__ == "__main__":
    main()
