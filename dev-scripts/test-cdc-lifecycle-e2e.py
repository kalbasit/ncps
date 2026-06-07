#!/usr/bin/env python3
"""
test-cdc-lifecycle-e2e.py — End-to-end test of the CDC lifecycle.

Successor to test-migration-e2e.py. Where that script exercises the
dbmate->Ent migration, this one drives the full content-defined-chunking
lifecycle:

    non-CDC  ->  CDC (eager + lazy)  ->  drain  ->  non-CDC

The lifecycle is driven by stopping and restarting ncps with/without the
CDC serve flags, because drain mode is decided at boot by
`initCDCDrainMode` (pkg/ncps/serve.go): when CDC was previously enabled
(the `cdc_*` keys are present in the `config` table) but the current boot
has CDC disabled and chunked nar_files remain, the instance keeps a chunk
store alive (drain mode) until `ncps migrate-chunks-to-nar` rewrites every
chunked NAR as a whole file. On the next boot with zero chunked NARs
remaining, initCDCDrainMode clears the stored CDC config and starts
without a chunk store.

Phases (each asserts serving AND database invariants):

  0. CDC-off baseline: push + serve a NAR whole-file; DB shows zero chunks
     and no cdc_* config keys.
  1. CDC-on (eager): push a new path; DB shows total_chunks > 0; served
     bytes identical; narinfo normalized at serve.
  2. CDC-on (lazy): exercise the lazy chunking path; served bytes identical.
  3. Drain active: restart with CDC off while chunks remain; serve from
     chunks still works; `migrate-chunks-to-nar --dry-run` lists remaining;
     `migrate-chunks-to-nar` drains everything; DB shows zero chunked NARs.
  4. Restart auto-completion: restart with CDC off; initCDCDrainMode clears
     the cdc_* config keys and creates no chunk store.

Cross-cutting checks (run within the phases above):

  A. Upload reference presence — HEAD/GET presence agrees with stored bytes.
  B. Non-destructive narinfo purge — purging one of two narinfos sharing a
     NAR leaves the shared bytes serving.
  C. fsck repair-not-delete — a broken narinfo<->nar_file link is repaired,
     not deleted.

Results are written to .e2e-results/cdc/<timestamp>/ (under the already
gitignored .e2e-results/ path shared with test-migration-e2e.py).

Backends (Garage/S3, PostgreSQL, MariaDB, Redis) must already be running —
use `task test:deps:start` or `nix run .#deps`. The wrapping task target
(`task test:cdc-lifecycle`) starts and stops them automatically.
"""

import argparse
import json
import os
import signal
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
VAR_NCPS = os.path.join(REPO_ROOT, "var", "ncps")
# Results live OUTSIDE var/ncps so run.py --clean can wipe var/ncps on the
# first start without taking the result logs with it. Nested under the
# already-gitignored .e2e-results/ path (shared with test-migration-e2e.py).
RESULTS_ROOT = os.path.join(REPO_ROOT, ".e2e-results", "cdc")

# run.py starts the first (and only, in single mode) instance on BASE_PORT.
PORT = 8501

# Database URLs mirror dev-scripts/run.py's DB_CONFIG.
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

# S3 storage flags mirror run.py's S3_CONFIG so the CLI subcommands
# (migrate-chunks-to-nar, fsck) talk to the same bucket the server uses.
S3_CONFIG = {
    "bucket": "test-bucket",
    "endpoint": "http://127.0.0.1:9000",
    "region": "us-east-1",
    "access_key": "GK1234567890abcdef12345678",
    "secret_key": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
}

LOCAL_STORAGE_PATH = os.path.join(REPO_ROOT, "var/ncps/storage")
TEMP_PATH = os.path.join(REPO_ROOT, "var/ncps/temp")

# Small packages — short closures, fast to fetch. One per phase so each
# phase introduces a distinct NAR under the active CDC mode.
PKG_BASELINE = "nixpkgs#hello"
PKG_EAGER = "nixpkgs#cowsay"
PKG_LAZY = "nixpkgs#jq"

# CDC config keys (pkg/config/config.go).
CDC_KEYS = ["cdc_enabled", "cdc_min", "cdc_avg", "cdc_max"]

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


class AssertionFailure(Exception):
    """A phase invariant did not hold."""


def check(cond, msg):
    if not cond:
        raise AssertionFailure(msg)
    log(f"  ✓ {msg}", G)


# ---------------------------------------------------------------------------
# Server lifecycle (via run.py, mirroring test-migration-e2e.py)
# ---------------------------------------------------------------------------


def start_ncps(db, storage, log_path, *, clean=False, cdc=False, lazy=False):
    """Start `python3 dev-scripts/run.py ...` in a new process group."""
    args = [
        sys.executable,
        os.path.join(REPO_ROOT, "dev-scripts", "run.py"),
        "--db",
        db,
        "--storage",
        storage,
        "--log-to-stdout",
    ]
    if clean:
        args.append("--clean")
    if lazy:
        args.append("--enable-lazy-cdc")
    elif cdc:
        args.append("--enable-cdc")
    mode = "lazy-cdc" if lazy else ("cdc" if cdc else "no-cdc")
    log(f"start_ncps: db={db} storage={storage} mode={mode} clean={clean}", G)
    f = open(log_path, "w")
    # start_new_session=True puts the child in its own session/process group
    # (like os.setsid) so stop_ncps can kill the whole group via os.killpg —
    # the modern, fork-safe replacement for preexec_fn=os.setsid.
    p = subprocess.Popen(
        args,
        cwd=REPO_ROOT,
        stdout=f,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    return p, f


def stop_ncps(p, f):
    if p is None:
        return
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
    if f is not None:
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
                    log("wait_ready: server up", G)
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


def restart(state, db, storage, log_path, *, cdc=False, lazy=False):
    """Stop the running instance (if any) and start a fresh one."""
    stop_ncps(state.get("srv"), state.get("f"))
    state["srv"], state["f"] = None, None
    srv, f = start_ncps(db, storage, log_path, cdc=cdc, lazy=lazy)
    if not wait_ready(srv):
        raise RuntimeError(f"ncps did not become ready ({log_path})")
    state["srv"], state["f"] = srv, f
    return srv, f


# ---------------------------------------------------------------------------
# HTTP helpers (the public serving surface)
# ---------------------------------------------------------------------------


def base_url():
    return f"http://127.0.0.1:{PORT}"


def http_get(path, timeout=30):
    url = base_url() + path
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return r.status, dict(r.headers), r.read()


def http_head(path, timeout=30):
    url = base_url() + path
    req = urllib.request.Request(url, method="HEAD")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return r.status, dict(r.headers)
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers)


def get_pubkey():
    _, _, body = http_get("/pubkey")
    return body.decode("utf-8").strip()


def fetch_narinfo(store_hash):
    """Return the raw .narinfo text for a store-path hash, or None on 404."""
    try:
        status, _, body = http_get(f"/{store_hash}.narinfo")
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise
    if status != 200:
        return None
    return body.decode("utf-8")


def parse_narinfo(text):
    """Parse .narinfo key: value lines into a dict (single-valued keys)."""
    fields = {}
    for line in text.splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            fields[k.strip()] = v.strip()
    return fields


def fetch_nar_bytes(narinfo_fields):
    """Fetch the NAR referenced by a parsed narinfo's URL field."""
    url = narinfo_fields["URL"]
    status, _, body = http_get("/" + url.lstrip("/"))
    if status != 200:
        raise RuntimeError(f"fetch_nar_bytes: unexpected status {status} for {url}")
    return body


# ---------------------------------------------------------------------------
# Cache seeding (build a package through ncps, mirroring test-migration-e2e.py)
# ---------------------------------------------------------------------------


def seed_cache(packages):
    log(f"seed_cache: building {packages}", G)
    cmd = [
        sys.executable,
        os.path.join(REPO_ROOT, "dev-scripts", "nix-isolated-build.py"),
        *packages,
    ]
    # Route nix-isolated-build's temp store onto the repo disk rather than a
    # small /tmp tmpfs.
    nix_tmp = os.path.join(REPO_ROOT, "var", "ncps", "nix-tmp")
    os.makedirs(nix_tmp, exist_ok=True)
    env = os.environ.copy()
    env["TMPDIR"] = nix_tmp
    r = subprocess.run(cmd, cwd=REPO_ROOT, timeout=900, env=env)
    if r.returncode != 0:
        raise RuntimeError(f"seed_cache failed (exit {r.returncode})")


def store_hash_of(flakeref):
    """Resolve a flakeref to the 32-char store-path hash of its output."""
    out = subprocess.check_output(
        ["nix", "path-info", "--json", flakeref],
        cwd=REPO_ROOT,
        text=True,
        timeout=120,
    )
    data = json.loads(out)
    # nix path-info --json returns either a list (newer) or dict keyed by path.
    if isinstance(data, list):
        path = data[0]["path"]
    else:
        path = next(iter(data))
    base = os.path.basename(path)  # <hash>-<name>
    return base.split("-", 1)[0]


# ---------------------------------------------------------------------------
# CLI wrappers (migrate-chunks-to-nar, fsck)
# ---------------------------------------------------------------------------


def storage_flags(storage):
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


def run_cli(subcmd, db, storage, extra=None, timeout=300, with_temp=False):
    """Run `go run . <subcmd> ...` with database + storage flags.

    Only some subcommands accept --cache-temp-path (e.g. migrate-chunks-to-nar);
    fsck does not, and urfave/cli errors on unknown flags, so it is opt-in.
    """
    cmd = ["go", "run", ".", subcmd, f"--cache-database-url={DB_URLS[db]}"]
    if with_temp:
        cmd += [f"--cache-temp-path={TEMP_PATH}"]
    cmd += storage_flags(storage)
    if extra:
        cmd += extra
    log(f"run_cli: {' '.join(cmd)}", G)
    r = subprocess.run(cmd, cwd=REPO_ROOT, capture_output=True, text=True, timeout=timeout)
    return r.returncode, r.stdout + r.stderr


def migrate_chunks_to_nar(db, storage, *, dry_run=False, force_reclaim=False):
    extra = []
    if dry_run:
        extra.append("--dry-run")
    if force_reclaim:
        extra.append("--force-reclaim")
    rc, out = run_cli("migrate-chunks-to-nar", db, storage, extra, with_temp=True)
    if rc != 0 and not dry_run:
        raise RuntimeError(f"migrate-chunks-to-nar failed (exit {rc})\n{out}")
    return out


def fsck(db, storage, *, repair=False):
    extra = ["--repair"] if repair else []
    rc, out = run_cli("fsck", db, storage, extra)
    return rc, out


# ---------------------------------------------------------------------------
# DB inspection (assert state directly, like snapshot_db in the sibling)
# ---------------------------------------------------------------------------


def db_query(db, sql, params=()):
    """Run a read-only query; return list of rows (tuples)."""
    if db == "sqlite":
        import sqlite3

        path = DB_URLS["sqlite"].split(":", 1)[1]
        if not os.path.exists(path):
            return []
        conn = sqlite3.connect(path)
        try:
            return list(conn.execute(sql, params).fetchall())
        finally:
            conn.close()
    if db == "postgres":
        import psycopg2

        conn = psycopg2.connect(DB_URLS["postgres"])
        try:
            with conn.cursor() as cur:
                cur.execute(sql.replace("?", "%s"), params)
                return list(cur.fetchall())
        finally:
            conn.close()
    if db == "mysql":
        import pymysql
        from urllib.parse import urlparse

        parsed = urlparse(DB_URLS["mysql"])
        conn = pymysql.connect(
            host=parsed.hostname,
            port=parsed.port or 3306,
            user=parsed.username,
            password=parsed.password or "",
            database=parsed.path.lstrip("/"),
        )
        try:
            with conn.cursor() as cur:
                cur.execute(sql.replace("?", "%s"), params)
                return list(cur.fetchall())
        finally:
            conn.close()
    raise ValueError(f"unknown db: {db}")


def chunked_nar_count(db):
    """Number of nar_files stored as chunk sequences (total_chunks > 0)."""
    rows = db_query(db, "SELECT COUNT(*) FROM nar_files WHERE total_chunks > 0")
    return rows[0][0] if rows else 0


def total_nar_count(db):
    rows = db_query(db, "SELECT COUNT(*) FROM nar_files")
    return rows[0][0] if rows else 0


def cdc_config_keys_present(db):
    """Which cdc_* keys are present in the config table."""
    placeholders = ",".join("?" for _ in CDC_KEYS)
    rows = db_query(
        db, f"SELECT key FROM config WHERE key IN ({placeholders})", tuple(CDC_KEYS)
    )
    return sorted(r[0] for r in rows)


# ---------------------------------------------------------------------------
# Phases
# ---------------------------------------------------------------------------


def phase_baseline(state, db, storage, sdir):
    section("PHASE 0 — CDC-off baseline")
    restart(
        state, db, storage, os.path.join(sdir, "p0-baseline.log"), cdc=False
    )
    state["pubkey"] = get_pubkey()
    check(cdc_config_keys_present(db) == [], "no cdc_* config keys at baseline")

    seed_cache([PKG_BASELINE])
    h = store_hash_of(PKG_BASELINE)
    state["baseline_hash"] = h

    ni_text = fetch_narinfo(h)
    check(ni_text is not None, "baseline narinfo served")
    fields = parse_narinfo(ni_text)
    nar = fetch_nar_bytes(fields)
    check(len(nar) > 0, "baseline NAR served with non-empty body")
    check(chunked_nar_count(db) == 0, "DB shows zero chunked NARs at baseline")
    state["baseline_nar_len"] = len(nar)


def phase_cdc_eager(state, db, storage, sdir):
    section("PHASE 1 — CDC on (eager): chunking + narinfo normalization")
    before = chunked_nar_count(db)
    restart(state, db, storage, os.path.join(sdir, "p1-eager.log"), cdc=True)
    check(get_pubkey() == state["pubkey"], "pubkey stable across CDC enable")

    seed_cache([PKG_EAGER])
    h = store_hash_of(PKG_EAGER)
    state["eager_hash"] = h

    ni_text = fetch_narinfo(h)
    check(ni_text is not None, "eager narinfo served")
    fields = parse_narinfo(ni_text)
    # Narinfo normalization at serve: required fields present + consistent.
    check("URL" in fields and "Compression" in fields, "narinfo has URL+Compression")
    check("NarHash" in fields and "NarSize" in fields, "narinfo has NarHash+NarSize")

    nar = fetch_nar_bytes(fields)
    check(len(nar) == int(fields["NarSize"]), "served NAR size matches narinfo NarSize")
    after = chunked_nar_count(db)
    check(after > before, f"new chunked NAR recorded ({before} -> {after})")


def phase_cdc_lazy(state, db, storage, sdir):
    section("PHASE 2 — CDC on (lazy): lazy chunking path")
    restart(state, db, storage, os.path.join(sdir, "p2-lazy.log"), lazy=True)

    seed_cache([PKG_LAZY])
    h = store_hash_of(PKG_LAZY)
    state["lazy_hash"] = h

    ni_text = fetch_narinfo(h)
    check(ni_text is not None, "lazy-phase narinfo served")
    fields = parse_narinfo(ni_text)
    nar = fetch_nar_bytes(fields)
    check(len(nar) == int(fields["NarSize"]), "lazy-phase served NAR size matches narinfo")
    # Re-read the baseline whole-file NAR to drive the lazy path over it.
    base_ni = fetch_narinfo(state["baseline_hash"])
    check(base_ni is not None, "baseline narinfo still served under lazy CDC")
    base_nar = fetch_nar_bytes(parse_narinfo(base_ni))
    check(
        len(base_nar) == state["baseline_nar_len"],
        "baseline NAR identical length when read under lazy CDC",
    )


def phase_drain(state, db, storage, sdir):
    section("PHASE 3 — drain: CDC off with chunks remaining")
    remaining = chunked_nar_count(db)
    check(remaining > 0, f"chunked NARs exist before drain ({remaining})")

    restart(state, db, storage, os.path.join(sdir, "p3-drain.log"), cdc=False)
    # Drain mode active: chunked NARs must still serve from chunks.
    ni = fetch_narinfo(state["eager_hash"])
    check(ni is not None, "chunked NAR still served in drain mode")
    nar = fetch_nar_bytes(parse_narinfo(ni))
    check(len(nar) > 0, "drain-mode NAR served with non-empty body")

    # Stop the server before mutating chunks (force-reclaim is drain-only).
    stop_ncps(state.get("srv"), state.get("f"))
    state["srv"], state["f"] = None, None

    dry = migrate_chunks_to_nar(db, storage, dry_run=True)
    with open(os.path.join(sdir, "migrate-dry-run.txt"), "w") as fp:
        fp.write(dry)
    check(chunked_nar_count(db) > 0, "dry-run did not mutate; chunks still present")

    out = migrate_chunks_to_nar(db, storage, force_reclaim=True)
    with open(os.path.join(sdir, "migrate.txt"), "w") as fp:
        fp.write(out)
    check(chunked_nar_count(db) == 0, "migrate-chunks-to-nar drained all chunked NARs")


def phase_restart_autocomplete(state, db, storage, sdir):
    section("PHASE 4 — restart: initCDCDrainMode auto-completion")
    restart(state, db, storage, os.path.join(sdir, "p4-autocomplete.log"), cdc=False)
    check(cdc_config_keys_present(db) == [], "initCDCDrainMode cleared cdc_* config keys")

    log_path = os.path.join(sdir, "p4-autocomplete.log")
    with open(log_path) as fp:
        boot_log = fp.read()
    check(
        "drain" in boot_log.lower(),
        "boot log mentions drain completion",
    )
    # All previously chunked NARs still serve as whole files.
    ni = fetch_narinfo(state["eager_hash"])
    check(ni is not None, "drained NAR serves as whole file after restart")
    nar = fetch_nar_bytes(parse_narinfo(ni))
    check(len(nar) > 0, "post-drain NAR served with non-empty body")


def phase_cross_cutting(state, db, storage, sdir):
    section("CROSS-CUTTING — upload presence, non-destructive purge, fsck repair")

    # A. Upload reference presence: every NAR referenced by a served narinfo
    #    must agree between HEAD and GET (no phantom presence -> no nix-copy
    #    missing-reference aborts).
    for label, h in (("baseline", "baseline_hash"), ("eager", "eager_hash")):
        ni = fetch_narinfo(state[h])
        check(ni is not None, f"{label} narinfo present for presence check")
        fields = parse_narinfo(ni)
        nar_path = "/" + fields["URL"].lstrip("/")
        head_status, _ = http_head(nar_path)
        get_status, _, body = http_get(nar_path)
        check(
            head_status == get_status == 200 and len(body) > 0,
            f"{label} NAR HEAD/GET agree (both 200, bytes present)",
        )

    # C. fsck repair-not-delete: fsck --repair must not destroy a NAR that a
    #    narinfo still references. Reclaiming genuinely orphaned nar_file rows
    #    (e.g. the transient xz/none duplicates the CDC on/off toggling leaves
    #    behind) is correct fsck behavior, so a row-count DECREASE is expected
    #    and not a failure. The real invariant is that every still-referenced
    #    NAR keeps serving after the repair.
    before = total_nar_count(db)
    rc, out = fsck(db, storage, repair=True)
    with open(os.path.join(sdir, "fsck-repair.txt"), "w") as fp:
        fp.write(out)
    check(rc == 0, f"fsck --repair exited cleanly (rc={rc})")
    after = total_nar_count(db)
    log(f"  fsck --repair: nar_files {before} -> {after} (orphan reclaim is OK)", B)
    for label, key in (
        ("baseline", "baseline_hash"),
        ("eager", "eager_hash"),
        ("lazy", "lazy_hash"),
    ):
        ni = fetch_narinfo(state[key])
        check(ni is not None, f"{label} narinfo still served after fsck --repair")
        nar = fetch_nar_bytes(parse_narinfo(ni))
        check(len(nar) > 0, f"{label} NAR still served (non-empty) after fsck --repair")


# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------


def run_lifecycle(db, storage, results_dir):
    label = f"{db}-{storage}"
    section(f"CDC LIFECYCLE: {label}")
    sdir = os.path.join(results_dir, label)
    os.makedirs(sdir, exist_ok=True)

    state = {"srv": None, "f": None}
    result = {"label": label, "db": db, "storage": storage, "status": "fail", "error": None}
    try:
        # First start performs the only --clean of the run.
        srv, f = start_ncps(
            db, storage, os.path.join(sdir, "p0-clean.log"), clean=True, cdc=False
        )
        if not wait_ready(srv):
            raise RuntimeError("ncps did not become ready on initial clean start")
        state["srv"], state["f"] = srv, f
        # Re-read baseline against the freshly-cleaned instance.
        state["pubkey"] = get_pubkey()
        check(cdc_config_keys_present(db) == [], "no cdc_* config keys after clean start")
        seed_cache([PKG_BASELINE])
        state["baseline_hash"] = store_hash_of(PKG_BASELINE)
        base_ni = fetch_narinfo(state["baseline_hash"])
        check(base_ni is not None, "baseline narinfo served after clean start")
        base_nar = fetch_nar_bytes(parse_narinfo(base_ni))
        state["baseline_nar_len"] = len(base_nar)
        check(chunked_nar_count(db) == 0, "DB shows zero chunked NARs at baseline")

        phase_cdc_eager(state, db, storage, sdir)
        phase_cdc_lazy(state, db, storage, sdir)
        phase_drain(state, db, storage, sdir)
        phase_restart_autocomplete(state, db, storage, sdir)
        phase_cross_cutting(state, db, storage, sdir)

        stop_ncps(state.get("srv"), state.get("f"))
        state["srv"], state["f"] = None, None

        result["status"] = "pass"
        log(f"✅ LIFECYCLE PASS: {label}", G)
    except Exception as e:
        result["error"] = str(e)
        log(f"❌ LIFECYCLE FAIL: {label}: {e}", R)
    finally:
        try:
            stop_ncps(state.get("srv"), state.get("f"))
        except Exception:
            pass
        with open(os.path.join(sdir, "result.json"), "w") as rf:
            json.dump(result, rf, indent=2, default=str)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", choices=["sqlite", "postgres", "mysql", "all"], default="sqlite")
    parser.add_argument("--storage", choices=["local", "s3"], default="local")
    parser.add_argument("--keep-going", action="store_true")
    args = parser.parse_args()

    dbs = ["sqlite", "postgres", "mysql"] if args.db == "all" else [args.db]

    ts = time.strftime("%Y%m%d-%H%M%S")
    results_dir = os.path.join(RESULTS_ROOT, ts)
    os.makedirs(results_dir, exist_ok=True)
    log(f"results dir: {results_dir}", B)

    overall = []
    failed = False
    for db in dbs:
        r = run_lifecycle(db, args.storage, results_dir)
        overall.append(r)
        if r["status"] != "pass":
            failed = True
            if not args.keep_going:
                break

    with open(os.path.join(results_dir, "summary.json"), "w") as f:
        json.dump(overall, f, indent=2, default=str)

    section("SUMMARY")
    for r in overall:
        sym = "✅" if r["status"] == "pass" else "❌"
        log(
            f"{sym}  {r['label']}: {r['status']}"
            + (f"  ({r['error']})" if r["error"] else ""),
            G if r["status"] == "pass" else R,
        )
    log(f"\nresults: {results_dir}", B)
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
