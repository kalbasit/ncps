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
    "postgresql://migration-user:migration-password@127.0.0.1:5432/migration-db?sslmode=disable"
)
MYSQL_URL = "mysql://migration-user:migration-password@127.0.0.1:3306/migration-db"

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
        return False


def run_dbmate(url):
    """
    Runs dbmate drop, create, and up for the given URL.
    Returns True on success.
    """
    try:
        # 1. Drop existing database (ignore errors if it doesn't exist)
        #    We suppress output to keep the console clean-ish, assuming
        #    'dbmate drop' failing is often acceptable (e.g. first run).
        subprocess.run(
            ["dbmate", "--url", url, "drop"],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

        # 2. Create fresh database
        subprocess.run(["dbmate", "--url", url, "create"], check=True)

        # 3. Run migrations
        subprocess.run(["dbmate", "--url", url, "up"], check=True)
        return True

    except subprocess.CalledProcessError as e:
        log(f"Migration failed during step '{e.cmd}': {e}", RED)
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
        # check_command logs the error
        print()
        return False


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
        # check_command logs the error
        print()
        return False


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
    # Run migrations sequentially and fail immediately on error
    if not migrate_postgres():
        sys.exit(1)

    if not migrate_mysql():
        sys.exit(1)

    if not migrate_sqlite():
        sys.exit(1)

    log("All requested migrations finished successfully.", GREEN)
    sys.exit(0)


if __name__ == "__main__":
    main()
