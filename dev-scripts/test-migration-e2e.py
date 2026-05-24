#!/usr/bin/env python3
"""
test-migration-e2e.py — End-to-end test of the dbmate→Ent migration.

For each (db, cdc, storage) scenario:

  1. Reset everything (var/ncps + databases + S3 bucket + Redis).
  2. git checkout main.
  3. Start ncps on main via run.py. Wait until /nix-cache-info responds.
  4. Seed cache by building small Nix packages through ncps.
  5. Capture baseline DB snapshot (row counts + narinfo hash set + pubkey).
  6. Stop ncps cleanly.
  7. git checkout the current (fix) branch.
  8. Run `go run . migrate up --dry-run` (record pending migrations).
  9. Run `go run . migrate up` (the function under test).
 10. Run `go run . migrate up --dry-run` again — must report none pending.
 11. Capture post-migration DB snapshot. Compare to baseline:
     - Every pre-migration narinfo hash must still be present.
 12. Restart ncps on the current branch via run.py (no --clean).
 13. Re-fetch one of the seed packages — proves the cache still serves
     pre-existing entries through the migrated schema.
 14. Build a *new* package — proves the write path works on the new schema.
 15. Stop ncps cleanly.

Results are written to .e2e-results/<timestamp>/.
"""

import argparse
import json
import os
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.request
from urllib.parse import urlparse

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
VAR_NCPS = os.path.join(REPO_ROOT, "var", "ncps")
STATE_FILE = os.path.join(VAR_NCPS, "state.json")
# Results live OUTSIDE var/ncps so reset_everything() can wipe var/ncps
# between scenarios without taking the result logs with it.
RESULTS_ROOT = os.path.join(REPO_ROOT, ".e2e-results")

PORT = 8501
MAIN_BRANCH = "main"

# Small packages — short closure, fast to fetch.
SEED_PACKAGES = ["nixpkgs#hello", "nixpkgs#cowsay"]
NEW_PACKAGE = "nixpkgs#jq"

S3 = {
    "endpoint": "http://127.0.0.1:9000",
    "bucket": "test-bucket",
    "access_key": "test-access-key",
    "secret_key": "test-secret-key",
}

DB_URLS = {
    "sqlite": f"sqlite:{REPO_ROOT}/var/ncps/db/db.sqlite",
    "postgres": "postgresql://dev-user:dev-password@127.0.0.1:5432/dev-db?sslmode=disable",
    "mysql": "mysql://dev-user:dev-password@127.0.0.1:3306/dev-db",
}

# Colors
G = "\033[0;32m"
Y = "\033[1;33m"
R = "\033[0;31m"
B = "\033[0;34m"
N = "\033[0m"


def log(msg, c=N):
    print(f"{c}{msg}{N}", flush=True)


def section(msg):
    bar = "=" * 78
    log(f"\n{bar}\n{msg}\n{bar}", B)


# ---------------------------------------------------------------------------
# Git helpers
# ---------------------------------------------------------------------------


def current_branch():
    return subprocess.check_output(
        ["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=REPO_ROOT, text=True
    ).strip()


def resolve_sha(ref):
    return subprocess.check_output(
        ["git", "rev-parse", ref], cwd=REPO_ROOT, text=True
    ).strip()


def git_checkout(ref):
    """
    Checkout `ref`. If `ref` is a branch name that is checked out in a
    different worktree (which would cause `git checkout` to fail), fall
    back to a detached-HEAD checkout of the same commit. Either form
    leaves the working tree exactly at that commit's content.
    """
    log(f"git checkout {ref}", B)
    r = subprocess.run(
        ["git", "checkout", ref],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
    )
    if r.returncode == 0:
        return
    if "already used by worktree" in (r.stderr or ""):
        sha = resolve_sha(ref)
        log(f"git_checkout: branch {ref!r} held by another worktree; "
            f"detaching at {sha[:12]}", Y)
        subprocess.run(
            ["git", "-c", "advice.detachedHead=false", "checkout", sha],
            cwd=REPO_ROOT,
            check=True,
        )
        return
    sys.stderr.write(r.stderr)
    r.check_returncode()


# ---------------------------------------------------------------------------
# Reset / cleanup
# ---------------------------------------------------------------------------


def reset_everything():
    """Wipe var/ncps, drop dev databases, recreate S3 bucket, flush Redis."""
    log("reset: removing var/ncps", Y)
    if os.path.exists(VAR_NCPS):
        shutil.rmtree(VAR_NCPS, ignore_errors=True)

    for engine in ("postgres", "mysql"):
        log(f"reset: dropping {engine} database", Y)
        subprocess.run(
            ["dbmate", "--url", DB_URLS[engine], "drop"],
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=15,
        )

    log("reset: recreating S3 bucket", Y)
    mc_env = os.environ.copy()
    parsed = urlparse(S3["endpoint"])
    mc_env["MC_HOST_e2e"] = (
        f"http://{S3['access_key']}:{S3['secret_key']}"
        f"@{parsed.hostname}:{parsed.port}"
    )
    subprocess.run(
        ["mc", "rb", "--force", f"e2e/{S3['bucket']}"],
        env=mc_env,
        check=False,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        timeout=15,
    )
    subprocess.run(
        ["mc", "mb", f"e2e/{S3['bucket']}"],
        env=mc_env,
        check=True,
        timeout=15,
    )

    log("reset: flushing Redis", Y)
    subprocess.run(
        ["redis-cli", "-h", "127.0.0.1", "-p", "6379", "flushall"],
        check=True,
        timeout=10,
    )


# ---------------------------------------------------------------------------
# Server lifecycle
# ---------------------------------------------------------------------------


def start_ncps(scenario, log_path):
    """Start `python3 dev-scripts/run.py ...` in a new process group."""
    args = [
        sys.executable,
        os.path.join(REPO_ROOT, "dev-scripts", "run.py"),
        "--db",
        scenario["db"],
        "--storage",
        scenario["storage"],
        "--log-to-stdout",
    ]
    if scenario["cdc"]:
        args.append("--enable-cdc")
    log(f"start_ncps: {' '.join(args)}", G)
    # When the worktree is detached at main's commit, run.py calls
    # `dbmate up` with no --migrations-dir. main's db/migrations/
    # contains only per-dialect sub-dirs; the historical
    # dbmate-wrapper (deleted in 80c4a15) used to inject the right
    # one. Without it, bare dbmate fails with `no migration files
    # found`. Setting DBMATE_MIGRATIONS_DIR here points dbmate at
    # the dialect-specific sub-dir. The variable is silently ignored
    # on the migration-branch side (where run.py uses `ncps migrate
    # up` instead), so this is safe to set unconditionally.
    env = os.environ.copy()
    env["DBMATE_MIGRATIONS_DIR"] = os.path.join(
        REPO_ROOT, "db", "migrations", scenario["db"]
    )
    f = open(log_path, "w")
    p = subprocess.Popen(
        args,
        cwd=REPO_ROOT,
        stdout=f,
        stderr=subprocess.STDOUT,
        env=env,
        preexec_fn=os.setsid,
    )
    return p, f


def stop_ncps(p, f):
    if p.poll() is None:
        try:
            os.killpg(os.getpgid(p.pid), signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            p.wait(timeout=30)
        except subprocess.TimeoutExpired:
            log("stop_ncps: SIGTERM timeout, sending SIGKILL", R)
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGKILL)
            except ProcessLookupError:
                pass
            p.wait(timeout=5)
    f.close()
    if not wait_port_close(PORT, timeout=15):
        raise RuntimeError(f"stop_ncps: port {PORT} still in use after 15s")


def wait_ready(proc, timeout=120):
    """Poll until ncps answers HTTP, or proc dies, or timeout expires."""
    deadline = time.time() + timeout
    url = f"http://127.0.0.1:{PORT}/nix-cache-info"
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(
                f"wait_ready: ncps exited with code {proc.returncode} "
                "before becoming ready"
            )
        try:
            with urllib.request.urlopen(url, timeout=2) as r:
                if r.status == 200:
                    log(f"wait_ready: server up after {time.time() - (deadline - timeout):.1f}s", G)
                    return True
        except Exception:
            pass
        time.sleep(1)
    return False


def wait_port_close(port, timeout=15):
    deadline = time.time() + timeout
    while time.time() < deadline:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(0.5)
        try:
            s.connect(("127.0.0.1", port))
            s.close()
            time.sleep(0.5)
        except (ConnectionRefusedError, OSError):
            return True
    log(f"wait_port_close: port {port} still listening after {timeout}s", R)
    return False


def get_pubkey():
    with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/pubkey", timeout=5) as r:
        return r.read().decode("utf-8").strip()


# ---------------------------------------------------------------------------
# Cache seeding
# ---------------------------------------------------------------------------


def seed_cache(packages):
    log(f"seed_cache: building {packages}", G)
    cmd = [
        sys.executable,
        os.path.join(REPO_ROOT, "dev-scripts", "nix-isolated-build.py"),
        *packages,
    ]
    # Route nix-isolated-build's temp store onto the real disk. By
    # default tempfile.mkdtemp() uses /tmp, which on this host is a
    # 16G tmpfs that fills up after a few scenarios' worth of
    # isolated nix stores. /home is on the spinning disk and has
    # plenty of room.
    nix_tmp = os.path.join(REPO_ROOT, "var", "ncps", "nix-tmp")
    os.makedirs(nix_tmp, exist_ok=True)
    env = os.environ.copy()
    env["TMPDIR"] = nix_tmp
    r = subprocess.run(cmd, cwd=REPO_ROOT, timeout=600, env=env)
    if r.returncode != 0:
        raise RuntimeError(f"seed_cache failed (exit {r.returncode})")


# ---------------------------------------------------------------------------
# Migration runner
# ---------------------------------------------------------------------------


def migrate_up(db_url, dry_run=False):
    cmd = ["go", "run", ".", "migrate", "up", f"--cache-database-url={db_url}"]
    if dry_run:
        cmd.append("--dry-run")
    log(f"migrate_up: {' '.join(cmd)}", G)
    r = subprocess.run(
        cmd,
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        timeout=120,
    )
    if r.returncode != 0:
        raise RuntimeError(
            f"migrate_up{'(--dry-run)' if dry_run else ''} failed (exit {r.returncode})\n"
            f"--- stdout ---\n{r.stdout}\n--- stderr ---\n{r.stderr}"
        )
    return r.stdout + r.stderr


# ---------------------------------------------------------------------------
# DB snapshot
# ---------------------------------------------------------------------------


def snapshot_db(db_url):
    """Return {tables: {name: row_count}, narinfo_hashes: sorted list}."""
    if db_url.startswith("sqlite:"):
        return _snapshot_sqlite(db_url.split(":", 1)[1])
    if db_url.startswith("postgresql:"):
        return _snapshot_pg(db_url)
    if db_url.startswith("mysql:"):
        return _snapshot_mysql(db_url)
    raise ValueError(f"unknown db url: {db_url}")


def _snapshot_sqlite(path):
    import sqlite3
    if not os.path.exists(path):
        return {"tables": {}, "narinfo_hashes": []}
    conn = sqlite3.connect(path)
    cur = conn.cursor()
    snap = {"tables": {}, "narinfo_hashes": []}
    cur.execute(
        "SELECT name FROM sqlite_master WHERE type='table' "
        "AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'schema_%' "
        "ORDER BY name"
    )
    tables = [r[0] for r in cur.fetchall()]
    for t in tables:
        cur.execute(f"SELECT COUNT(*) FROM `{t}`")
        snap["tables"][t] = cur.fetchone()[0]
    if "narinfos" in tables:
        cur.execute("SELECT hash FROM narinfos ORDER BY hash")
        snap["narinfo_hashes"] = [r[0] for r in cur.fetchall()]
    conn.close()
    return snap


def _snapshot_pg(url):
    import psycopg2
    snap = {"tables": {}, "narinfo_hashes": []}
    conn = psycopg2.connect(url)
    try:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT tablename FROM pg_tables WHERE schemaname='public' "
                "ORDER BY tablename"
            )
            tables = [r[0] for r in cur.fetchall()]
            for t in tables:
                cur.execute(f'SELECT COUNT(*) FROM "{t}"')
                snap["tables"][t] = cur.fetchone()[0]
            if "narinfos" in tables:
                cur.execute("SELECT hash FROM narinfos ORDER BY hash")
                snap["narinfo_hashes"] = [r[0] for r in cur.fetchall()]
    finally:
        conn.close()
    return snap


def _snapshot_mysql(url):
    import pymysql
    parsed = urlparse(url)
    snap = {"tables": {}, "narinfo_hashes": []}
    conn = pymysql.connect(
        host=parsed.hostname,
        port=parsed.port or 3306,
        user=parsed.username,
        password=parsed.password or "",
        database=parsed.path.lstrip("/"),
    )
    try:
        with conn.cursor() as cur:
            cur.execute("SHOW TABLES")
            tables = sorted(r[0] for r in cur.fetchall())
            for t in tables:
                cur.execute(f"SELECT COUNT(*) FROM `{t}`")
                snap["tables"][t] = cur.fetchone()[0]
            if "narinfos" in tables:
                cur.execute("SELECT hash FROM narinfos ORDER BY hash")
                snap["narinfo_hashes"] = [r[0] for r in cur.fetchall()]
    finally:
        conn.close()
    return snap


def compare(baseline, after):
    """Return (data_loss: bool, summary: dict)."""
    base_hashes = set(baseline.get("narinfo_hashes", []))
    after_hashes = set(after.get("narinfo_hashes", []))
    missing = sorted(base_hashes - after_hashes)
    new = sorted(after_hashes - base_hashes)
    summary = {
        "baseline_count": len(base_hashes),
        "after_count": len(after_hashes),
        "missing": missing,
        "new": new,
        "baseline_tables": baseline.get("tables", {}),
        "after_tables": after.get("tables", {}),
    }
    return (len(missing) > 0), summary


# ---------------------------------------------------------------------------
# Scenario runner
# ---------------------------------------------------------------------------


def run_scenario(scenario, fix_branch, results_dir):
    label = f"{scenario['db']}-cdc{'on' if scenario['cdc'] else 'off'}-{scenario['storage']}"
    section(f"SCENARIO {label}")
    sdir = os.path.join(results_dir, label)
    os.makedirs(sdir, exist_ok=True)

    db_url = DB_URLS[scenario["db"]]
    result = {"label": label, "scenario": scenario, "status": "fail", "error": None, "stages": {}}
    srv = None
    f = None
    try:
        # Stage 1: reset
        reset_everything()

        # Stage 2: main
        git_checkout(MAIN_BRANCH)
        srv, f = start_ncps(scenario, os.path.join(sdir, "main-server.log"))
        if not wait_ready(srv):
            raise RuntimeError("ncps did not become ready on main")

        result["stages"]["main_pubkey"] = get_pubkey()
        seed_cache(SEED_PACKAGES)
        baseline = snapshot_db(db_url)
        result["stages"]["baseline"] = baseline
        with open(os.path.join(sdir, "baseline.json"), "w") as bf:
            json.dump(baseline, bf, indent=2)

        # Stage 3: stop main
        stop_ncps(srv, f)
        srv, f = None, None

        # Stage 4: checkout fix branch + migrate
        git_checkout(fix_branch)

        dry1 = migrate_up(db_url, dry_run=True)
        result["stages"]["dry_run_before"] = dry1
        with open(os.path.join(sdir, "dry-run-before.txt"), "w") as df:
            df.write(dry1)

        migrate_out = migrate_up(db_url, dry_run=False)
        result["stages"]["migrate_out"] = migrate_out
        with open(os.path.join(sdir, "migrate-up.txt"), "w") as mf:
            mf.write(migrate_out)

        dry2 = migrate_up(db_url, dry_run=True)
        result["stages"]["dry_run_after"] = dry2
        # Parse "pending versions: N" from the dry-run output. After a
        # successful migrate up, this MUST be 0; anything else means
        # apply didn't actually run all the migrations.
        import re
        m = re.search(r"pending versions:\s*(\d+)", dry2)
        if m is None:
            raise RuntimeError(
                f"idempotency check: dry-run output did not contain "
                f"'pending versions: N': {dry2!r}"
            )
        if int(m.group(1)) != 0:
            raise RuntimeError(
                f"idempotency check failed: {m.group(1)} migration(s) still "
                f"pending after migrate up"
            )

        # Stage 5: snapshot + compare
        after = snapshot_db(db_url)
        result["stages"]["after"] = after
        with open(os.path.join(sdir, "after.json"), "w") as af:
            json.dump(after, af, indent=2)

        data_loss, summary = compare(baseline, after)
        result["stages"]["compare"] = summary
        if data_loss:
            raise RuntimeError(
                f"DATA LOSS: {len(summary['missing'])} narinfo hashes "
                f"missing after migration (was {summary['baseline_count']}, "
                f"now {summary['after_count']})"
            )

        # Stage 6: restart on fix branch (no --clean)
        srv, f = start_ncps(scenario, os.path.join(sdir, "fix-server.log"))
        if not wait_ready(srv):
            raise RuntimeError("ncps did not become ready on fix branch")

        new_pubkey = get_pubkey()
        result["stages"]["fix_pubkey"] = new_pubkey
        if new_pubkey != result["stages"]["main_pubkey"]:
            raise RuntimeError(
                f"PUBKEY CHANGED across migration. before={result['stages']['main_pubkey']!r} "
                f"after={new_pubkey!r}"
            )

        # Stage 7: cache-hit verification (re-fetch a seed package)
        seed_cache([SEED_PACKAGES[0]])

        # Stage 8: write-path test (new package)
        seed_cache([NEW_PACKAGE])

        after_write = snapshot_db(db_url)
        result["stages"]["after_write"] = after_write
        if after_write.get("tables", {}).get("narinfos", 0) <= after.get("tables", {}).get("narinfos", 0):
            log("WARN: narinfo count did not grow after new package build", Y)

        # Stage 9: stop
        stop_ncps(srv, f)
        srv, f = None, None

        result["status"] = "pass"
        log(f"✅ SCENARIO PASS: {label}", G)
    except Exception as e:
        result["error"] = str(e)
        log(f"❌ SCENARIO FAIL: {label}: {e}", R)
    finally:
        if srv is not None:
            try:
                stop_ncps(srv, f)
            except Exception:
                pass
        with open(os.path.join(sdir, "result.json"), "w") as rf:
            json.dump(result, rf, indent=2, default=str)
    return result


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--db", choices=["sqlite", "postgres", "mysql", "all"], default="all"
    )
    parser.add_argument(
        "--cdc", choices=["on", "off", "both"], default="both"
    )
    parser.add_argument(
        "--storage", choices=["local", "s3", "both"], default="both"
    )
    parser.add_argument("--keep-going", action="store_true")
    args = parser.parse_args()

    fix_branch = current_branch()
    if fix_branch == MAIN_BRANCH:
        log("error: cannot run from main; checkout the migration branch first.", R)
        sys.exit(2)
    if fix_branch == "HEAD":
        log("error: started in detached HEAD; checkout a named branch first.", R)
        sys.exit(2)

    dbs = [args.db] if args.db != "all" else ["sqlite", "postgres", "mysql"]
    cdcs = (
        [args.cdc == "on"]
        if args.cdc != "both"
        else [False, True]
    )
    storages = [args.storage] if args.storage != "both" else ["local", "s3"]

    scenarios = [
        {"db": d, "cdc": c, "storage": s}
        for d in dbs
        for c in cdcs
        for s in storages
    ]

    ts = time.strftime("%Y%m%d-%H%M%S")
    results_dir = os.path.join(RESULTS_ROOT, ts)
    os.makedirs(results_dir, exist_ok=True)
    log(f"results dir: {results_dir}", B)

    overall = []
    failed = False
    try:
        for s in scenarios:
            r = run_scenario(s, fix_branch, results_dir)
            overall.append(r)
            if r["status"] != "pass":
                failed = True
                if not args.keep_going:
                    break
    finally:
        if current_branch() != fix_branch:
            try:
                git_checkout(fix_branch)
            except Exception:
                pass

    with open(os.path.join(results_dir, "summary.json"), "w") as f:
        json.dump(overall, f, indent=2, default=str)

    section("SUMMARY")
    for r in overall:
        sym = "✅" if r["status"] == "pass" else "❌"
        log(f"{sym}  {r['label']}: {r['status']}" + (f"  ({r['error']})" if r["error"] else ""),
            G if r["status"] == "pass" else R)
    log(f"\nresults: {results_dir}", B)

    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
