"""The ``serve`` phase: push a NAR through ncps, serve it, prove byte-identity.

The simplest end-to-end check, and the smoke test for the whole local-mode
architecture: seed a small package through ncps, fetch its narinfo + NAR from
every replica, and assert each served NAR decompresses to the canonical
``nix-store --dump`` content (so same-size corruption fails even on HTTP 200).
"""

from __future__ import annotations

import os

from client import canonical_nar_sha256, hash_of_store_path, realise_package
from harness_config import check, log, section

# Small package — short closure, fast to fetch through ncps.
SERVE_PKG = "nixpkgs#hello"

# Budget for time-to-first-byte on a warm NAR read, in seconds.
#
# A NAR that is already in the store must begin streaming promptly. In production
# this exact path — serving a NAR already present in storage — stalled ~57s in a
# single uncancellable stat on an NFS mount, and the ingress (60s read timeout)
# aborted the response mid-body, handing the client a 200 with a truncated body.
# Every byte was correct; only the latency was wrong, so byte-comparison alone
# scored it a PASS.
#
# The budget is deliberately far below any reverse-proxy read timeout and far
# above a healthy read (8-300ms observed), so it flags pathology without being
# fragile on a loaded CI runner.
TTFB_BUDGET_SECONDS = float(os.environ.get("NCPS_E2E_TTFB_BUDGET_SECONDS", "15"))

# The client timeout must exceed the budget, so a stall is reported as a measured
# budget violation rather than an opaque client timeout with no number attached.
TTFB_CLIENT_TIMEOUT = int(TTFB_BUDGET_SECONDS * 6)


def run(deployment, scenario) -> None:
    section(f"SERVE — {scenario.name}")

    # Realise on the host for canonical bytes; ncps serves the same path by
    # pulling it from upstream when the narinfo/NAR is requested below.
    store_path = realise_package(SERVE_PKG)
    store_hash = hash_of_store_path(store_path)
    canonical = canonical_nar_sha256(store_path)

    digests = []
    for i, _ in enumerate(deployment.replica_urls()):
        c = deployment.client(i)
        ni_text = c.fetch_narinfo(store_hash)
        check(ni_text is not None, f"replica {i}: narinfo served for {store_hash}")
        fields = c.parse_narinfo(ni_text)
        digest, raw_len = c.served_nar_digest(fields)
        check(raw_len > 0, f"replica {i}: NAR served with non-empty body")
        check(
            digest == canonical,
            f"replica {i}: served NAR is byte-identical to the canonical store-path NAR",
        )
        digests.append(digest)

        # Re-fetch the now-warm NAR and measure time-to-first-byte. This is the
        # production failure shape: the NAR is present and correct, but the first
        # byte arrives too late to survive a reverse proxy.
        timed = c.get_timed("/" + fields["URL"].lstrip("/"), timeout=TTFB_CLIENT_TIMEOUT)
        log(
            f"replica {i}: warm NAR ttfb={timed.ttfb_seconds:.3f}s "
            f"total={timed.total_seconds:.3f}s bytes={len(timed.body)} "
            f"(budget {TTFB_BUDGET_SECONDS:.1f}s)"
        )
        check(timed.status == 200, f"replica {i}: warm NAR re-read returned 200")
        check(
            timed.ttfb_seconds < TTFB_BUDGET_SECONDS,
            f"replica {i}: warm NAR time-to-first-byte {timed.ttfb_seconds:.3f}s "
            f"is within the {TTFB_BUDGET_SECONDS:.1f}s budget "
            f"(a byte-correct but slow response is a FAILURE, not a pass)",
        )

    if len(digests) > 1:
        check(len(set(digests)) == 1, "all replicas served byte-identical NARs")
