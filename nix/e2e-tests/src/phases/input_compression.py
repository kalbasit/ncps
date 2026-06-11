"""The ``input-compression`` phase: eager-CDC compressed-request mislabel (#1398).

Reproduces GitHub issue #1398 ("input compression not recognized on
0.10.0-rc13"). Under eager CDC, fetching a narinfo starts a background
pull-through that downloads the upstream ``.nar.xz``, decompresses it into a
``none`` temp file, and chunks it. While that download/chunk window is open, a
client whose narinfo still advertises ``Compression: xz`` requests
``/nar/<hash>.nar.xz``. ncps piggybacks on the in-flight (none-temp) download and
relabels the request to ``Compression: none`` (cache.go:1459), streaming the
decompressed bytes — optionally re-wrapped in transport zstd (server.go:897).
The client, which expects xz per its narinfo, receives bytes it cannot xz-decode
and fails with ``input compression not recognized``.

Correct behavior for a ``.nar.xz`` request is EITHER a valid xz body OR HTTP 404
(so the client falls back to an upstream that still has the original file) — but
NEVER a 200 whose body is not xz. This phase asserts that invariant.

Topology: single replica, in-flight staging OFF (the local/sqlite locker is not
distributed) — the exact reporter setup, where the staging path that would 404 a
compressed request never engages. Local-only: the in-flight window is a timing
event (like ``staging-contention``), so — reusing that phase's machinery — it
uses a large NAR (so the download/chunk window lasts seconds), DB-gates the race
to the open window (``total_chunks`` not yet set), and clean-restart-retries a
bounded number of times if a run misses the window. A ``.nar.xz`` request fired
while the holder is in-flight is the one that 404s or, on a buggy build, is
served mislabeled.
"""

from __future__ import annotations

import lzma
import threading
import time
import urllib.error
import urllib.request

from client import hash_of_store_path, realise_package
from harness_config import AssertionFailure, B, R, Y, check, log, section

# A NAR large enough that the eager-CDC download+decompress+chunk window lasts
# several seconds on a fast host (so concurrent .nar.xz requests reliably overlap
# it), and substitutable from the configured upstream. gcc-unwrapped (~several
# hundred MiB, Hydra-built on cache.nixos.org) is the same package the
# staging-contention phase uses for the identical "open an in-flight window" need.
PACKAGE = "nixpkgs#gcc-unwrapped"

# Per-attempt: how long to keep firing the .nar.xz variant, and the concurrency.
HAMMER_SECONDS = 8.0
HAMMER_THREADS = 8

# Bounded retries: a run can miss the in-flight window (the NAR fully chunked
# before any .nar.xz request lands). That is a timing loss, not a result — clean
# restart and retry, failing only if the window is never overlapped.
ATTEMPTS = 4


def _inflight_state(db, nar_hash: str) -> str:
    """Classify eager-CDC materialization from the ``nar_files`` row.

    ``absent`` (download in progress, no chunk row yet) or ``inflight``
    (``total_chunks == 0``, actively chunking) both mean the in-flight window is
    OPEN; ``done`` (``total_chunks > 0``) means it has closed.
    """
    rows = db.query("SELECT total_chunks FROM nar_files WHERE hash = ?", (nar_hash,))
    if not rows:
        return "absent"
    tc = rows[0][0] or 0
    return "done" if tc > 0 else "inflight"


def _xz_path_from_none_url(nar_url: str) -> str:
    """``nar/<h>.nar`` (predictive-none) -> ``/nar/<h>.nar.xz`` (compressed variant).

    The predictive-none narinfo URL is ``nar/<h>.nar``; the compressed variant the
    reporter's client requests is the SAME hash with a ``.xz`` suffix appended
    (``nar/<h>.nar.xz``) — not ``.nar`` replaced by ``.xz``.
    """
    p = "/" + nar_url.lstrip("/")
    if p.endswith(".nar.xz"):
        return p
    if p.endswith(".nar"):
        return p + ".xz"
    # narinfo already advertises some other compression: swap to .nar.xz.
    return p.split(".nar", 1)[0] + ".nar.xz"


def _classify_xz(base_url: str, xz_path: str) -> str:
    """Fetch ``xz_path`` and classify: 'notfound' | 'valid' | 'mislabeled'.

    'mislabeled' is the #1398 bug: a 200 whose body is not decodable as xz
    (the client-visible "input compression not recognized").
    """
    # urlopen raises HTTPError for any non-2xx/3xx status (404, 5xx), so a return
    # from the with-block means a 2xx (200 for a NAR GET) and `body` is set.
    try:
        with urllib.request.urlopen(base_url + xz_path, timeout=120) as r:
            body = r.read()
    except urllib.error.HTTPError:
        return "notfound"
    except Exception:  # noqa: BLE001 — connection resets etc. are not the invariant under test
        return "notfound"
    if not body:
        return "mislabeled"
    try:
        lzma.decompress(body)
    except lzma.LZMAError:
        return "mislabeled"
    return "valid"


def _race_xz_during_window(deployment, nar_hash: str, xz_path: str) -> dict:
    """Hammer ``xz_path`` concurrently while the holder is in-flight.

    Stops as soon as the DB shows the NAR fully chunked (window closed) or the
    time budget expires. Returns verdict counts plus whether the window was
    actually overlapped (so a pure-404 'never opened' run is distinguishable from
    a genuine all-404 pass).
    """
    base = deployment.replica_urls()[0]
    db = deployment.db()
    counts = {"valid": 0, "notfound": 0, "mislabeled": 0}
    lock = threading.Lock()
    overlapped = {"seen": False}
    deadline = time.time() + HAMMER_SECONDS

    def worker():
        while time.time() < deadline:
            # Serialize the DB gate under the shared lock: db.query opens its own
            # sqlite connection per call, but the lock avoids 8 threads churning
            # connections in lockstep and keeps DB access single-threaded.
            with lock:
                in_flight = _inflight_state(db, nar_hash) != "done"
            if in_flight:
                overlapped["seen"] = True
            verdict = _classify_xz(base, xz_path)
            with lock:
                counts[verdict] += 1

    threads = [threading.Thread(target=worker, daemon=True) for _ in range(HAMMER_THREADS)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    counts["overlapped"] = overlapped["seen"]
    return counts


def run(deployment, scenario) -> None:
    section("INPUT-COMPRESSION — eager-CDC compressed-request mislabel (#1398)")

    # Eager CDC, single replica, in-flight staging OFF (local locker) — the
    # reporter's topology.
    deployment.restart(cdc=True)

    # Canonical store hash from the host; ncps pulls the same path through from
    # the configured upstream (whose narinfo advertises xz).
    store_path = realise_package(PACKAGE)
    store_hash = hash_of_store_path(store_path)
    log(f"  target {PACKAGE} (store hash {store_hash})", B)

    for attempt in range(1, ATTEMPTS + 1):
        c = deployment.client(0)

        # Prime the narinfo: under eager CDC this fires the background prefetch
        # (the holder) that opens the in-flight window, and yields the none URL.
        ni = None
        for _ in range(30):
            ni = c.fetch_narinfo(store_hash)
            if ni:
                break
            time.sleep(1)
        check(ni is not None, "narinfo served (NAR still uncached)")
        fields = c.parse_narinfo(ni)
        nar_url = fields.get("URL", "")
        check(bool(nar_url), "narinfo has a NAR URL")
        nar_hash = nar_url.rsplit("/", 1)[-1].split(".", 1)[0]
        xz_path = _xz_path_from_none_url(nar_url)

        log(f"  attempt {attempt}/{ATTEMPTS}: racing .nar.xz ({xz_path}) during in-flight window", B)
        counts = _race_xz_during_window(deployment, nar_hash, xz_path)
        log(
            f"    .nar.xz responses — valid: {counts['valid']}, "
            f"404(fallback): {counts['notfound']}, mislabeled: {counts['mislabeled']} "
            f"(window overlapped: {counts['overlapped']})",
            B,
        )

        # Any mislabeled response reproduces #1398 — fail immediately.
        check(
            counts["mislabeled"] == 0,
            f"every .nar.xz response is valid xz or 404 — got {counts['mislabeled']} "
            f"mislabeled 200 response(s): an uncompressed body served under a .nar.xz "
            f"URL is the client-visible 'input compression not recognized'",
        )

        # No mislabel AND we genuinely overlapped the in-flight window → the
        # invariant held under the conditions that trigger the bug → done.
        if counts["overlapped"]:
            log("  in-flight window overlapped; .nar.xz never served mislabeled", B)
            return

        # Missed the window (NAR chunked before any .nar.xz landed): retry clean.
        if attempt < ATTEMPTS:
            log("  in-flight window missed; clean restart + retry", Y)
            deployment.clean_restart(cdc=True)

    raise AssertionFailure(
        f"never overlapped the eager-CDC in-flight window after {ATTEMPTS} attempts — "
        f"the compressed-request mislabel could not be exercised (try a larger PACKAGE "
        f"or longer HAMMER_SECONDS). Not a pass: the invariant was never tested under "
        f"the conditions that trigger #1398."
    )
