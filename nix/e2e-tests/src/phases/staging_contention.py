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
import time

from client import (
    canonical_nar_sha256,
    decode_nar,
    hash_of_store_path,
    realise_package,
)
from harness_config import AssertionFailure, B, Y, check, log, section

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


# Bounded retries for the chunking window: a missed in-flight window (the NAR
# fully chunked before any reader could contend) is a transient timing loss, not
# an ncps defect, so we restart clean and try again — but only a bounded number
# of times so a genuine non-activation still FAILs.
def _hash_from_nar_url(nar_url: str) -> str:
    """Extract the NAR hash from a ``nar/<hash>.nar[.xz]`` URL."""
    return nar_url.rsplit("/", 1)[-1].split(".", 1)[0]


def _inflight_state(db, nar_hash: str) -> str:
    """Classify eager-CDC materialization from the ``nar_files`` row.

    - ``absent``  : no row yet — the download is in progress before the chunk
      row is created (the bulk of the in-flight window).
    - ``inflight``: a row with ``total_chunks == 0`` — actively chunking, the
      download lock is still held.
    - ``done``    : ``total_chunks > 0`` — fully chunked, the in-flight window
      has already closed.
    """
    rows = db.query("SELECT total_chunks FROM nar_files WHERE hash = ?", (nar_hash,))
    if not rows:
        return "absent"
    tc = rows[0][0] or 0
    return "done" if tc > 0 else "inflight"


def _await_inflight(db, nar_hash: str) -> str:
    """Decide whether to race now to overlap an in-flight eager-CDC download.

    Returns ``"missed"`` only when the NAR is already fully chunked
    (``total_chunks > 0`` — the in-flight window has closed); otherwise
    ``"inflight"``. Crucially we race on ``absent`` (download in progress, chunk
    row not yet created) as well as on an actively-chunking row, so the readers
    overlap the long *download* phase. Waiting for the chunk row (``total_chunks
    == 0``) would fire only after the download finishes, leaving the brief
    post-download chunk phase too short to reliably contend.
    """
    return "missed" if _inflight_state(db, nar_hash) == "done" else "inflight"


def _window_setup(deployment, *, cdc: bool) -> tuple:
    """Section header, canonical reference, and per-replica config assertions."""
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
    return store_hash, canonical


def _prime(deployment, store_hash: str, *, cdc: bool) -> tuple:
    """Fetch the narinfo (learning the NAR URL + Compression).

    Under eager CDC this also triggers the fire-and-forget background prefetch
    that opens the in-flight window the chunking-window race must overlap.
    """
    c = deployment.client(0)
    ni = None
    for _ in range(30):
        ni = c.fetch_narinfo(store_hash)
        if ni:
            break
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
    return nar_url, comp


def _assert_race_content(race, canonical: str) -> None:
    """Every reader returned a complete NAR byte-identical to the canonical one."""
    ok = [r for r in race if r and r["status"] == 200]
    check(len(ok) == CLIENTS, f"all {CLIENTS} readers returned HTTP 200")
    digests = {r["digest"] for r in race if r}
    check(len(digests) == 1, "all readers received an identical NAR digest")
    check(
        digests == {canonical},
        "served NAR decompresses byte-identical to canonical `nix-store --dump`",
    )


def _run_download_window(deployment) -> None:
    """Download window (CDC off): the readers' race IS the first NAR request, so
    a plain prime-then-race already overlaps the in-flight whole-file download."""
    store_hash, canonical = _window_setup(deployment, cdc=False)
    nar_url, comp = _prime(deployment, store_hash, cdc=False)
    log(f"  racing {CLIENTS} clients on {nar_url} (Compression={comp})", B)
    t0 = time.monotonic()
    race = _race_fetch(deployment, nar_url, comp, CLIENTS)
    log(f"  race completed in {time.monotonic() - t0:.1f}s", B)
    _assert_race_content(race, canonical)
    activated_on = _scan_logs(deployment, STAGING_ACTIVATION_LOG)
    if not activated_on:
        contended = _scan_logs(deployment, LOCK_CONTENTION_LOG)
        producer_error = _scan_logs(deployment, PRODUCER_ERROR_LOG)
        raise AssertionFailure(
            "in-flight staging did not activate in the download window "
            f"(contended={contended}, producer_error={producer_error}); a no-op "
            "run is a failure — use a larger --package or more clients so readers "
            "actually race an in-flight download"
        )
    check(True, f"in-flight staging activated (replicas {activated_on})")


# Bounded retries for the chunking window: a run can miss the in-flight window
# (the NAR fully materializes before any reader contends), which would leave
# staging un-exercised. Rather than silently pass such a no-op, restart from a
# clean state and retry; fail only after every attempt misses.
CHUNKING_ATTEMPTS = 3


def _run_chunking_window(deployment) -> None:
    """Chunking window (eager CDC): with predictive-`none` the cross-pod reader
    requests the uncompressed `.nar`, and ncps now routes an uncompressed
    actively-chunking read through download coordination so it **engages in-flight
    staging** (contends, records a staging request, serves from staging) rather
    than the fragile progressive chunk reassembly. Race readers while the
    download+chunk is in flight and assert staging activates on the non-holder,
    with byte-identical NARs. Retry from a clean state a bounded number of times if
    a run misses the in-flight window."""
    store_hash, canonical = _window_setup(deployment, cdc=True)

    for attempt in range(1, CHUNKING_ATTEMPTS + 1):
        t0 = time.monotonic()
        nar_url, comp = _prime(deployment, store_hash, cdc=True)
        log(f"  narinfo primed in {time.monotonic() - t0:.1f}s (url {nar_url})", B)

        if _await_inflight(deployment.db(), _hash_from_nar_url(nar_url)) == "missed":
            # The window is already closed: racing now is guaranteed not to engage
            # staging, so skip the slow/heavy race and retry from a clean state
            # immediately rather than wasting it on a result we would discard.
            log("  in-flight window already closed before racing", Y)
            if attempt < CHUNKING_ATTEMPTS:
                log(
                    f"  attempt {attempt}/{CHUNKING_ATTEMPTS}: clean restart + retry immediately",
                    Y,
                )
                deployment.clean_restart(cdc=True)
                continue
        else:
            log("  in-flight window open — racing while the holder downloads/chunks", B)

        log(f"  racing {CLIENTS} clients on {nar_url} (Compression={comp})", B)
        t0 = time.monotonic()
        race = _race_fetch(deployment, nar_url, comp, CLIENTS)
        log(f"  race completed in {time.monotonic() - t0:.1f}s", B)

        # Every reader must receive a complete, byte-identical NAR regardless of
        # which serve path was taken — a truncated/differing body fails even on 200.
        _assert_race_content(race, canonical)

        activated_on = _scan_logs(deployment, STAGING_ACTIVATION_LOG)
        if activated_on:
            check(True, f"in-flight staging activated (replicas {activated_on})")
            return

        if attempt < CHUNKING_ATTEMPTS:
            log(
                f"  attempt {attempt}/{CHUNKING_ATTEMPTS}: staging did not activate; "
                "clean restart + retry",
                Y,
            )
            deployment.clean_restart(cdc=True)

    contended = _scan_logs(deployment, LOCK_CONTENTION_LOG)
    producer_error = _scan_logs(deployment, PRODUCER_ERROR_LOG)
    raise AssertionFailure(
        f"in-flight staging did not activate in the chunking window after "
        f"{CHUNKING_ATTEMPTS} attempts (contended={contended}, "
        f"producer_error={producer_error}); the eager-CDC uncompressed cross-pod read "
        "must engage in-flight staging, not the fragile progressive chunk reassembly"
    )


def run(deployment, scenario) -> None:
    # Window 1 — download (CDC off): use the initial clean provision (cdc off).
    _run_download_window(deployment)
    # Window 2 — chunking (CDC on): clean restart so the NAR is uncached again.
    deployment.clean_restart(cdc=True)
    _run_chunking_window(deployment)
