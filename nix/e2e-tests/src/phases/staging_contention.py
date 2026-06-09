"""The ``staging-contention`` phase: prove in-flight NAR staging activates.

Ported from ``dev-scripts/test-inflight-staging-contention-e2e.py``. Races N
concurrent clients (spread across replicas) fetching the SAME large uncached NAR
so the lock-losing waiters become staging consumers, then asserts every reader
received a complete NAR byte-identical to the canonical ``nix-store --dump`` and
that staging actually ACTIVATED — a no-op run is a FAILURE, not a pass. Both
protected windows are exercised as independently-scored runs: the download
window (CDC off, whole-file) and the chunking window (CDC on).
"""

from __future__ import annotations

import hashlib
import threading

from client import (
    canonical_nar_sha256,
    decode_nar,
    hash_of_store_path,
    realise_package,
)
from harness_config import AssertionFailure, B, check, log, section

STAGING_ACTIVATION_LOG = (
    "in-flight staging parts available, serving from staging while peer downloads"
)
LOCK_CONTENTION_LOG = "failed to acquire download lock"
PRODUCER_ERROR_LOG = "in-flight staging producer stopped with error"

# Needs a NAR large enough to outlast a few staging poll ticks and exceed the
# 8 MiB part size, and substitutable from the configured upstream. gcc-unwrapped
# (~several hundred MiB, Hydra-built on cache.nixos.org) clears both bars.
PACKAGE = "nixpkgs#gcc-unwrapped"
CLIENTS = 8


def _race_fetch(deployment, nar_url: str, comp: str, clients: int):
    """Fire `clients` concurrent NAR GETs (spread across replicas) at once."""
    urls = deployment.replica_urls()
    barrier = threading.Barrier(clients)
    results = [None] * clients
    path = "/" + nar_url.lstrip("/")

    def worker(idx: int, base_url: str):
        rec = {"base": base_url, "status": None, "length": 0, "digest": None, "error": None}
        try:
            barrier.wait(timeout=60)
            import urllib.request

            with urllib.request.urlopen(base_url + path, timeout=900) as r:
                body = r.read()
                rec["status"] = r.status
                rec["length"] = len(body)
                if r.status == 200:
                    rec["digest"] = hashlib.sha256(decode_nar(body, comp)).hexdigest()
        except Exception as e:  # noqa: BLE001 — recorded as a per-client failure
            rec["error"] = str(e)
        results[idx] = rec

    threads = []
    for idx in range(clients):
        base = urls[idx % len(urls)]
        t = threading.Thread(target=worker, args=(idx, base), daemon=True)
        threads.append(t)
        t.start()
    for t in threads:
        t.join()
    return results


def _scan_logs(deployment, needle: str):
    """Replica indexes whose log contains `needle`."""
    hits = []
    for i, _ in enumerate(deployment.replica_urls()):
        if needle in deployment.logs(i):
            hits.append(i)
    return hits


def _run_window(deployment, *, cdc: bool) -> None:
    window = "chunking" if cdc else "download"
    section(f"CONTENTION WINDOW: {window} (cdc={cdc})")

    # Canonical reference from the LOCAL store, independent of what ncps serves.
    store_path = realise_package(PACKAGE)
    store_hash = hash_of_store_path(store_path)
    canonical = canonical_nar_sha256(store_path)
    log(f"  canonical NAR digest: {canonical[:16]}… (path {store_path})", B)

    # Effective per-replica config from state.json.
    state = deployment.read_state()
    check(state.get("locker") == "redis", "effective locker is redis")
    check(state.get("inflight_staging") is True, "effective inflight_staging is true")
    check(
        len(state.get("instances", [])) == len(deployment.replica_urls()),
        "all replicas recorded in state.json",
    )

    # Prime the narinfo once (NAR stays uncached); learn the NAR URL + Compression.
    c = deployment.client(0)
    ni = None
    for _ in range(30):
        ni = c.fetch_narinfo(store_hash)
        if ni:
            break
        import time

        time.sleep(1)
    check(ni is not None, "narinfo served (NAR still uncached)")
    fields = c.parse_narinfo(ni)
    nar_url = fields["URL"]
    comp = fields.get("Compression", "none")
    check(bool(nar_url), "narinfo has a NAR URL")
    if cdc:
        check(
            comp == "none",
            f"eager-CDC chunking-window narinfo advertises Compression: none (got {comp!r})",
        )
        check(
            nar_url.endswith(".nar") and not nar_url.endswith(".nar.xz"),
            f"eager-CDC narinfo URL is the uncompressed .nar (got {nar_url!r})",
        )

    log(f"  racing {CLIENTS} clients on {nar_url} (Compression={comp})", B)
    race = _race_fetch(deployment, nar_url, comp, CLIENTS)

    ok = [r for r in race if r and r["status"] == 200]
    check(len(ok) == CLIENTS, f"all {CLIENTS} readers returned HTTP 200")
    digests = {r["digest"] for r in race if r}
    check(len(digests) == 1, "all readers received an identical NAR digest")
    check(
        digests == {canonical},
        "served NAR decompresses byte-identical to canonical `nix-store --dump`",
    )

    activated_on = _scan_logs(deployment, STAGING_ACTIVATION_LOG)
    if not activated_on:
        contended = _scan_logs(deployment, LOCK_CONTENTION_LOG)
        producer_error = _scan_logs(deployment, PRODUCER_ERROR_LOG)
        raise AssertionFailure(
            f"in-flight staging did not activate in the {window} window "
            f"(contended={contended}, producer_error={producer_error}); a no-op "
            "run is a failure — use a larger --package or more clients so readers "
            "actually race an in-flight download"
        )
    check(True, f"in-flight staging activated (replicas {activated_on})")


def run(deployment, scenario) -> None:
    # Window 1 — download (CDC off): use the initial clean provision (cdc off).
    _run_window(deployment, cdc=False)
    # Window 2 — chunking (CDC on): clean restart so the NAR is uncached again.
    deployment.clean_restart(cdc=True)
    _run_window(deployment, cdc=True)
