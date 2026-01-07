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
    """Runs dbmate up for the given URL. Returns True on success."""
    try:
        subprocess.run(["dbmate", "--url", url, "up"], check=True)
        return True
    except subprocess.CalledProcessError:
        log("Migration failed", RED)
        return False


def migrate_postgres():
    log("1. Migrating PostgreSQL...", YELLOW)

    parsed = urlparse(POSTGRES_URL)
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
        if run_dbmate(POSTGRES_URL):
            log("PostgreSQL migration complete.", GREEN)
            print()
            return True
        else:
            print()
            return False
    else:
        log("Skipped PostgreSQL.", RED)
        print()
        # If the service isn't running, we consider this "skipped" rather than "failed"
        return True


def migrate_mysql():
    log("2. Migrating MySQL...", YELLOW)

    parsed = urlparse(MYSQL_URL)
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
        if run_dbmate(MYSQL_URL):
            log("MySQL migration complete.", GREEN)
            print()
            return True
        else:
            print()
            return False
    else:
        log("Skipped MySQL.", RED)
        print()
        return True


def migrate_sqlite():
    log("3. Migrating SQLite...", YELLOW)

    if os.path.isdir(SQLITE_DIR):
        log("✓ SQLite directory ready", GREEN)
        if run_dbmate(SQLITE_URL):
            log("SQLite migration complete.", GREEN)
            print()
            return True
        else:
            print()
            return False
    else:
        log("SQLite directory error", RED)
        print()
        return False


def main():
    log("Starting migrations for all detected database configurations...", BLUE)
    print()

    # Collect results
    results = {
        "PostgreSQL": migrate_postgres(),
        "MySQL": migrate_mysql(),
        "SQLite": migrate_sqlite(),
    }

    # Determine final exit status
    if all(results.values()):
        log("All requested migrations finished successfully.", GREEN)
        sys.exit(0)
    else:
        log("Some migrations failed:", RED)
        for name, success in results.items():
            if not success:
                log(f"  - {name} failed", RED)
        sys.exit(1)


if __name__ == "__main__":
    main()
