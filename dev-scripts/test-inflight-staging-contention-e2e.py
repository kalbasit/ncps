#!/usr/bin/env python3
"""
test-inflight-staging-contention-e2e.py — End-to-end test of in-flight NAR
staging under real multi-replica contention.

In-flight NAR staging (pkg/cache/inflight_staging*.go) is the fix for the
#660 download-window and #1289 chunking-window "incomplete NAR served under
contention" bugs. It only *activates* when a second reader races an in-flight
download: the lock-losing waiter is fed from committed staging part-objects
instead of a half-written stream. The feature requires a distributed locker
(`--locker redis`) and self-disables on the local locker.

Go unit tests cover the staging logic in-process; the k8s HA permutations merely
set `inflightStaging.enabled = true` without driving concurrent same-NAR fetches.
Nothing actually *activates* the feature end-to-end. This driver does:

  1. Launch >=2 ncps replicas via dev-scripts/run.py with
     `--locker redis --inflight-staging` against shared storage.
  2. Confirm each replica's effective config (state.json): locker=redis,
     inflight_staging=true.
  3. Race N concurrent clients (spread across replicas) fetching the SAME large,
     uncached NAR so the lock-loser(s) become staging waiters.
  4. Assert every reader received a COMPLETE NAR whose decompressed content is
     byte-identical to the canonical store-path NAR (`nix-store --dump`) and to
     every other reader — a truncated/short/differing body fails even on HTTP 200.
  5. Prove staging actually ACTIVATED (per-replica debug log line); a no-op run
     where staging never engaged is reported as a FAILURE, not a pass.

Two windows are exercised (separately, with independent pass/fail):
  - download window  (CDC off, whole-file NARs)
  - chunking window  (CDC on, --enable-cdc)
across the `local` (shared path) and `s3` storage backends.

Backends (Garage/S3, PostgreSQL, Redis) must already be running — use
`nix run .#deps` (fixed ports). The wrapping task target
(`task test:inflight-staging-contention`) starts and stops them automatically.
Note: `task test:deps:start` allocates RANDOM ports and is NOT compatible with
this fixed-port driver.

This driver builds/fetches real NARs over the network through ncps, so it is a
manual/opt-in dev tool and is NOT part of `nix flake check`.
"""

import argparse
import hashlib
import io
import json
import lzma
import os
import signal
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
VAR_NCPS = os.path.join(REPO_ROOT, "var", "ncps")
STATE_FILE = os.path.join(VAR_NCPS, "state.json")
LOG_DIR = os.path.join(REPO_ROOT, "var", "log")
# Results live OUTSIDE var/ncps so run.py --clean can wipe var/ncps on the first
# start without taking the result logs with it. Nested under the already
# gitignored .e2e-results/ path.
RESULTS_ROOT = os.path.join(REPO_ROOT, ".e2e-results", "inflight-staging")

# run.py assigns replica ports as BASE_PORT + (i - 1).
BASE_PORT = 8501

# The exact debug message ncps emits when a lock-losing waiter detects staging
# parts and begins serving from staging (pkg/cache/cache.go). This is the
# black-box proof that staging ACTIVATED. run.py defaults to --log-level debug,
# so it lands in var/log/ncps-<port>.log without enabling Prometheus.
STAGING_ACTIVATION_LOG = (
    "in-flight staging parts available, serving from staging while peer downloads"
)
# Diagnostics for a non-activation failure: proof the race happened (a waiter
# lost the download lock) and proof the staging producer tried-but-errored (the
# holder's temp file was already gone — pkg/cache/inflight_staging.go).
LOCK_CONTENTION_LOG = "failed to acquire download lock"
PRODUCER_ERROR_LOG = "in-flight staging producer stopped with error"

# Staging commits parts of the streamed NAR at the Go default part size (8 MiB),
# so activation needs a NAR comfortably larger than that — enough parts must
# commit while the holder is still downloading for a waiter to observe them.
# nixpkgs#go (~216 MiB NAR) clears that bar with margin. Override with --package;
# if staging does not activate, the run fails and suggests a larger package.
DEFAULT_PACKAGE = "nixpkgs#go"

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
# Cluster lifecycle (via run.py)
# ---------------------------------------------------------------------------


def ports_for(replicas):
    return [BASE_PORT + i for i in range(replicas)]


def start_cluster(db, storage, replicas, log_path, *, cdc=False):
    """Start `python3 dev-scripts/run.py ...` (HA) in a new process group.

    run.py blocks on signal.pause() after spawning the replicas, so it is run as
    a background process group that stop_cluster() later signals.
    """
    args = [
        sys.executable,
        os.path.join(REPO_ROOT, "dev-scripts", "run.py"),
        "--clean",
        "--db",
        db,
        "--storage",
        storage,
        "--locker",
        "redis",
        "--inflight-staging",
        "--replicas",
        str(replicas),
        "--log-to-stdout",
    ]
    if cdc:
        args.append("--enable-cdc")
    log(
        f"start_cluster: db={db} storage={storage} replicas={replicas} "
        f"cdc={cdc} locker=redis inflight-staging=on",
        G,
    )
    f = open(log_path, "w")
    # start_new_session=True isolates the child in its own process group so
    # stop_cluster can kill the whole tree via os.killpg.
    p = subprocess.Popen(
        args,
        cwd=REPO_ROOT,
        stdout=f,
        stderr=subprocess.STDOUT,
        start_new_session=True,
    )
    return p, f


def stop_cluster(p, f, ports):
    if p is not None and p.poll() is None:
        try:
            os.killpg(os.getpgid(p.pid), signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            p.wait(timeout=30)
        except subprocess.TimeoutExpired:
            log("stop_cluster: SIGTERM timeout, sending SIGKILL", R)
            try:
                os.killpg(os.getpgid(p.pid), signal.SIGKILL)
            except ProcessLookupError:
                pass
            p.wait(timeout=5)
    if f is not None:
        f.close()
    for port in ports:
        if not wait_port_close(port, timeout=15):
            raise RuntimeError(f"stop_cluster: port {port} still in use after 15s")


def wait_all_ready(proc, ports, timeout=180):
    """Poll until every replica answers HTTP, or proc dies, or timeout expires."""
    deadline = time.time() + timeout
    pending = set(ports)
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(
                f"wait_all_ready: run.py exited with code {proc.returncode} "
                "before all replicas became ready"
            )
        for port in list(pending):
            try:
                url = f"http://127.0.0.1:{port}/nix-cache-info"
                with urllib.request.urlopen(url, timeout=2) as r:
                    if r.status == 200:
                        pending.discard(port)
            except Exception:
                pass
        if not pending:
            log(f"wait_all_ready: all {len(ports)} replicas up", G)
            return True
        time.sleep(1)
    log(f"wait_all_ready: replicas still down after {timeout}s: {sorted(pending)}", R)
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


def read_state():
    with open(STATE_FILE) as fp:
        return json.load(fp)


# ---------------------------------------------------------------------------
# HTTP helpers (the public serving surface)
# ---------------------------------------------------------------------------


def http_get(port, path, timeout=300):
    url = f"http://127.0.0.1:{port}{path}"
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return r.status, dict(r.headers), r.read()


def fetch_narinfo(port, store_hash, *, retries=30):
    """Fetch + parse the .narinfo for a store-path hash, retrying transient 5xx.

    The first request triggers ncps to pull the narinfo from upstream and store
    it centrally; it does NOT fetch the NAR body (that happens on the NAR GET),
    so the NAR stays uncached for the contention race that follows.
    """
    last = None
    for _ in range(retries):
        try:
            status, _, body = http_get(port, f"/{store_hash}.narinfo", timeout=60)
            if status == 200:
                return parse_narinfo(body.decode("utf-8"))
            last = f"status {status}"
        except urllib.error.HTTPError as e:
            if e.code == 404:
                raise RuntimeError(
                    f"narinfo 404 for {store_hash}: package not substitutable "
                    "from the configured upstream"
                ) from e
            last = f"HTTP {e.code}"
        except Exception as e:  # noqa: BLE001 — transient startup races
            last = str(e)
        time.sleep(1)
    raise RuntimeError(f"fetch_narinfo: giving up on {store_hash} ({last})")


def parse_narinfo(text):
    fields = {}
    for line in text.splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            fields[k.strip()] = v.strip()
    return fields


def decode_nar(raw, comp):
    """Return the decompressed NAR bytes for the narinfo Compression value."""
    if comp in ("none", ""):
        return raw
    if comp == "xz":
        return lzma.decompress(raw)
    if comp in ("zst", "zstd"):
        import zstandard

        return zstandard.ZstdDecompressor().stream_reader(io.BytesIO(raw)).read()
    raise RuntimeError(f"decode_nar: unsupported compression {comp!r}")


# ---------------------------------------------------------------------------
# Canonical store-path NAR (independent ground truth)
# ---------------------------------------------------------------------------


def realise_package(package):
    """Ensure the package is in the local nix store so we can dump its NAR."""
    log(f"realise_package: nix build {package}", G)
    nix_tmp = os.path.join(VAR_NCPS, "nix-tmp")
    os.makedirs(nix_tmp, exist_ok=True)
    env = os.environ.copy()
    env["TMPDIR"] = nix_tmp
    r = subprocess.run(
        ["nix", "build", "--no-link", "--print-out-paths", package],
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        timeout=900,
        env=env,
    )
    if r.returncode != 0:
        raise RuntimeError(f"nix build {package} failed:\n{r.stderr}")
    return r.stdout.strip().splitlines()[-1].strip()


def store_hash_of(store_path):
    """The 32-char store-path hash (the narinfo key ncps serves under)."""
    return os.path.basename(store_path).split("-", 1)[0]


def canonical_nar_digest(store_path):
    """sha256 of `nix-store --dump <path>` — the canonical NAR serialization.

    A served NAR, once decompressed, MUST equal this regardless of the
    Compression ncps applied or whether it was reassembled from CDC chunks.
    """
    raw = subprocess.check_output(
        ["nix-store", "--dump", store_path], cwd=REPO_ROOT, timeout=300
    )
    return hashlib.sha256(raw).hexdigest()


# ---------------------------------------------------------------------------
# Contention race
# ---------------------------------------------------------------------------


def race_fetch(ports, narinfo_url, comp, clients):
    """Fire `clients` concurrent NAR GETs (spread across replicas) at once.

    A threading.Barrier aligns every request so the first triggers the upstream
    download and the rest become staging waiters. Returns a list of per-client
    dicts: {port, status, length, digest}.
    """
    barrier = threading.Barrier(clients)
    results = [None] * clients
    path = "/" + narinfo_url.lstrip("/")

    def worker(idx, port):
        rec = {"port": port, "status": None, "length": 0, "digest": None, "error": None}
        try:
            barrier.wait(timeout=60)
            status, _, body = http_get(port, path, timeout=600)
            rec["status"] = status
            rec["length"] = len(body)
            if status == 200:
                rec["digest"] = hashlib.sha256(decode_nar(body, comp)).hexdigest()
        except Exception as e:  # noqa: BLE001 — recorded as a per-client failure
            rec["error"] = str(e)
        results[idx] = rec

    threads = []
    for idx in range(clients):
        port = ports[idx % len(ports)]
        t = threading.Thread(target=worker, args=(idx, port), daemon=True)
        threads.append(t)
        t.start()
    for t in threads:
        t.join()
    return results


def scan_logs(ports, needle):
    """Replicas whose log file contains `needle`."""
    hits = []
    for port in ports:
        try:
            with open(os.path.join(LOG_DIR, f"ncps-{port}.log")) as fp:
                if needle in fp.read():
                    hits.append(port)
        except FileNotFoundError:
            pass
    return hits


# ---------------------------------------------------------------------------
# Phase runner
# ---------------------------------------------------------------------------


def run_phase(db, storage, replicas, clients, cdc, package, results_dir):
    window = "chunking" if cdc else "download"
    label = f"{storage}-{window}"
    section(f"CONTENTION PHASE: {label} (db={db}, replicas={replicas}, clients={clients})")
    sdir = os.path.join(results_dir, label)
    os.makedirs(sdir, exist_ok=True)
    ports = ports_for(replicas)

    result = {
        "label": label,
        "db": db,
        "storage": storage,
        "window": window,
        "replicas": replicas,
        "clients": clients,
        "package": package,
        "status": "fail",
        "error": None,
    }
    proc = f = None
    try:
        # Canonical reference is computed from the LOCAL store, independent of
        # anything ncps serves.
        store_path = realise_package(package)
        store_hash = store_hash_of(store_path)
        canonical = canonical_nar_digest(store_path)
        log(f"  canonical NAR digest: {canonical[:16]}… (path {store_path})", B)

        proc, f = start_cluster(db, storage, replicas, os.path.join(sdir, "cluster.log"), cdc=cdc)
        if not wait_all_ready(proc, ports):
            raise RuntimeError("cluster did not become ready")

        # Spec: confirm effective per-replica config from state.json.
        state = read_state()
        check(state.get("locker") == "redis", "effective locker is redis")
        check(state.get("inflight_staging") is True, "effective inflight_staging is true")
        check(len(state.get("instances", [])) == replicas, f"{replicas} replicas recorded in state.json")

        # Prime the narinfo once (NAR stays uncached), learn the NAR URL.
        fields = fetch_narinfo(ports[0], store_hash)
        nar_url = fields["URL"]
        comp = fields.get("Compression", "none")
        check(bool(nar_url), "narinfo served with a NAR URL (NAR still uncached)")

        # Drive the contention race.
        log(f"  racing {clients} clients on {nar_url} (Compression={comp})", B)
        race = race_fetch(ports, nar_url, comp, clients)
        with open(os.path.join(sdir, "race.json"), "w") as rf:
            json.dump(race, rf, indent=2)

        # Spec: every reader complete + byte-identical to canonical and to peers.
        ok_status = [r for r in race if r["status"] == 200]
        check(len(ok_status) == clients, f"all {clients} readers returned HTTP 200")
        digests = {r["digest"] for r in race}
        check(len(digests) == 1, "all readers received an identical NAR digest")
        check(
            digests == {canonical},
            "served NAR decompresses byte-identical to canonical `nix-store --dump`",
        )

        # Spec: staging must have ACTIVATED — a no-op run is a failure. On
        # non-activation, classify WHY so the failure is self-explanatory:
        #   - producer_error: the race happened and staging tried, but the
        #     producer errored (e.g. the holder's temp file was already gone) —
        #     a real ncps finding, not a harness artifact.
        #   - contended-only: a waiter lost the lock but staging never engaged —
        #     widen the download window (holder polls for waiters every 1s) with
        #     a larger --package whose NAR download outlasts a few poll ticks.
        activated_on = scan_logs(ports, STAGING_ACTIVATION_LOG)
        if not activated_on:
            contended = scan_logs(ports, LOCK_CONTENTION_LOG)
            producer_error = scan_logs(ports, PRODUCER_ERROR_LOG)
            result["diagnosis"] = {
                "contended_replicas": contended,
                "producer_error_replicas": producer_error,
            }
            if producer_error:
                hint = (
                    "the staging producer errored before it could serve (see WARN "
                    "'in-flight staging producer stopped with error' in the replica "
                    "logs) — this is a real ncps finding, not a harness issue"
                )
            elif contended:
                hint = (
                    "a waiter lost the download lock but staging never engaged; the "
                    "holder polls for waiters every 1s, so use a larger --package "
                    "whose NAR download outlasts a few poll ticks"
                )
            else:
                hint = (
                    "no lock contention was observed; increase --clients or use a "
                    "larger --package so the readers actually race an in-flight download"
                )
            raise AssertionFailure(
                f"in-flight staging did not activate "
                f"(contended={contended}, producer_error={producer_error}); {hint}"
            )
        check(True, f"in-flight staging activated (replicas {activated_on})")

        result["status"] = "pass"
        result["activated_on"] = activated_on
        log(f"✅ PHASE PASS: {label}", G)
    except Exception as e:
        result["error"] = str(e)
        log(f"❌ PHASE FAIL: {label}: {e}", R)
    finally:
        try:
            stop_cluster(proc, f, ports)
        except Exception as e:  # noqa: BLE001
            msg = f"cleanup stop_cluster failed: {e}"
            log(f"❌ {msg}", R)
            result["status"] = "fail"
            result["error"] = f"{result['error']}; {msg}" if result["error"] else msg
        with open(os.path.join(sdir, "result.json"), "w") as rf:
            json.dump(result, rf, indent=2, default=str)
    return result


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    # HA requires a non-sqlite database (run.py guard); default postgres.
    parser.add_argument("--db", choices=["postgres", "mysql"], default="postgres")
    parser.add_argument(
        "--storage", choices=["local", "s3", "both"], default="both",
        help="Storage backend(s) to exercise",
    )
    parser.add_argument(
        "--window", choices=["download", "chunking", "both"], default="both",
        help="download = CDC off (whole-file), chunking = CDC on",
    )
    parser.add_argument("--replicas", type=int, default=2)
    parser.add_argument("--clients", type=int, default=6, help="Concurrent racing readers")
    parser.add_argument(
        "--package", default=DEFAULT_PACKAGE,
        help="Flakeref whose NAR is raced; use a larger NAR if staging never activates",
    )
    parser.add_argument("--keep-going", action="store_true")
    args = parser.parse_args()

    if args.replicas < 2:
        log("error: contention needs >=2 replicas (HA requires the redis locker).", R)
        sys.exit(2)
    if args.clients < 2:
        log("error: contention needs >=2 concurrent clients.", R)
        sys.exit(2)

    storages = ["local", "s3"] if args.storage == "both" else [args.storage]
    windows = (
        [False, True]
        if args.window == "both"
        else [args.window == "chunking"]
    )

    ts = time.strftime("%Y%m%d-%H%M%S")
    results_dir = os.path.join(RESULTS_ROOT, ts)
    os.makedirs(results_dir, exist_ok=True)
    log(f"results dir: {results_dir}", B)

    overall = []
    failed = False
    for storage in storages:
        for cdc in windows:
            r = run_phase(
                args.db, storage, args.replicas, args.clients, cdc, args.package, results_dir
            )
            overall.append(r)
            if r["status"] != "pass":
                failed = True
                if not args.keep_going:
                    break
        if failed and not args.keep_going:
            break

    with open(os.path.join(results_dir, "summary.json"), "w") as fp:
        json.dump(overall, fp, indent=2, default=str)

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
