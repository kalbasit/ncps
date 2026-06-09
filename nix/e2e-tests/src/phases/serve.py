"""The ``serve`` phase: push a NAR through ncps, serve it, prove byte-identity.

The simplest end-to-end check, and the smoke test for the whole local-mode
architecture: seed a small package through ncps, fetch its narinfo + NAR from
every replica, and assert each served NAR decompresses to the canonical
``nix-store --dump`` content (so same-size corruption fails even on HTTP 200).
"""

from __future__ import annotations

from client import canonical_nar_sha256, hash_of_store_path, realise_package
from harness_config import check, section

# Small package — short closure, fast to fetch through ncps.
SERVE_PKG = "nixpkgs#hello"


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

    if len(digests) > 1:
        check(len(set(digests)) == 1, "all replicas served byte-identical NARs")
