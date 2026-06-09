"""The ``cdc-lifecycle`` phase: non-CDC -> CDC (eager+lazy) -> drain -> non-CDC.

Ported from ``dev-scripts/test-cdc-lifecycle-e2e.py``. Drives the full
content-defined-chunking lifecycle by restarting ncps with/without the CDC
serve flags (drain mode is decided at boot by ``initCDCDrainMode``), asserting
serving correctness and DB invariants at every phase. Operates through the
``Deployment`` adapter, so the same body runs wherever the adapter can restart
ncps and run ``migrate-chunks-to-nar`` / ``fsck``.

Note: since #1380 eager CDC advertises ``Compression: none`` predictively at
store time, so a freshly-seeded eager NAR's narinfo is deterministically the
uncompressed ``.nar``.
"""

from __future__ import annotations

import re
import time

from client import seed_cache, store_hash_of
from harness_config import CDC_KEYS, B, check, log, section

PKG_BASELINE = "nixpkgs#hello"
PKG_EAGER = "nixpkgs#cowsay"
PKG_LAZY = "nixpkgs#jq"


# -- DB invariant helpers ------------------------------------------------------


def _chunked_nar_count(db) -> int:
    return db.scalar("SELECT COUNT(*) FROM nar_files WHERE total_chunks > 0") or 0


def _total_nar_count(db) -> int:
    return db.scalar("SELECT COUNT(*) FROM nar_files") or 0


def _quote_ident(dialect: str, name: str) -> str:
    # `key` is reserved in MySQL/MariaDB (backticks); sqlite/postgres use "..."
    return f"`{name}`" if dialect == "mysql" else f'"{name}"'


def _cdc_config_keys_present(db) -> list:
    placeholders = ",".join("?" for _ in CDC_KEYS)
    key = _quote_ident(db.dialect, "key")
    rows = db.query(
        f"SELECT {key} FROM config WHERE {key} IN ({placeholders})", tuple(CDC_KEYS)
    )
    return sorted(r[0] for r in rows)


# -- phase body ----------------------------------------------------------------


def run(deployment, scenario) -> None:
    db = deployment.db()
    c = deployment.client(0)
    state = {}

    # PHASE 0 — CDC-off baseline (provisioned clean with CDC off).
    section("PHASE 0 — CDC-off baseline")
    check(_cdc_config_keys_present(db) == [], "no cdc_* config keys at baseline")
    seed_cache([PKG_BASELINE])
    state["baseline_hash"] = store_hash_of(PKG_BASELINE)
    ni = c.fetch_narinfo(state["baseline_hash"])
    check(ni is not None, "baseline narinfo served")
    fields = c.parse_narinfo(ni)
    nar = c.fetch_nar_bytes(fields)
    check(len(nar) > 0, "baseline NAR served with non-empty body")
    check(_chunked_nar_count(db) == 0, "DB shows zero chunked NARs at baseline")
    state["baseline_digest"], _ = c.served_nar_digest(fields)

    # PHASE 1 — CDC on (eager): chunking + predictive narinfo normalization.
    section("PHASE 1 — CDC on (eager)")
    before = _chunked_nar_count(db)
    deployment.restart(cdc=True)
    c = deployment.client(0)
    seed_cache([PKG_EAGER])
    state["eager_hash"] = store_hash_of(PKG_EAGER)
    ni = c.fetch_narinfo(state["eager_hash"])
    check(ni is not None, "eager narinfo served")
    fields = c.parse_narinfo(ni)
    check("URL" in fields and "Compression" in fields, "narinfo has URL+Compression")
    check("NarHash" in fields and "NarSize" in fields, "narinfo has NarHash+NarSize")
    check(
        fields["Compression"] == "none",
        f"eager CDC narinfo advertises Compression: none (got {fields['Compression']!r})",
    )
    check(
        fields["URL"].endswith(".nar") and not fields["URL"].endswith(".nar.xz"),
        f"eager CDC narinfo URL is the uncompressed .nar (got {fields['URL']!r})",
    )
    nar = c.fetch_nar_bytes(fields)
    check(len(nar) == int(fields["NarSize"]), "served NAR size matches narinfo NarSize")
    state["eager_digest"], _ = c.served_nar_digest(fields)
    check(_chunked_nar_count(db) > before, f"new chunked NAR recorded ({before} -> after)")

    # PHASE 2 — CDC on (lazy).
    section("PHASE 2 — CDC on (lazy)")
    deployment.restart(lazy=True)
    c = deployment.client(0)
    seed_cache([PKG_LAZY])
    state["lazy_hash"] = store_hash_of(PKG_LAZY)
    ni = c.fetch_narinfo(state["lazy_hash"])
    check(ni is not None, "lazy-phase narinfo served")
    fields = c.parse_narinfo(ni)
    nar = c.fetch_nar_bytes(fields)
    check(len(nar) == int(fields["NarSize"]), "lazy-phase served NAR size matches narinfo")
    state["lazy_digest"], _ = c.served_nar_digest(fields)
    base_ni = c.fetch_narinfo(state["baseline_hash"])
    check(base_ni is not None, "baseline narinfo still served under lazy CDC")
    digest, _ = c.served_nar_digest(c.parse_narinfo(base_ni))
    check(digest == state["baseline_digest"], "baseline NAR content identical under lazy CDC")

    # PHASE 3 — drain: CDC off with chunks remaining.
    section("PHASE 3 — drain")
    remaining = _chunked_nar_count(db)
    check(remaining > 0, f"chunked NARs exist before drain ({remaining})")
    deployment.restart(cdc=False)
    c = deployment.client(0)
    ni = c.fetch_narinfo(state["eager_hash"])
    check(ni is not None, "chunked NAR still served in drain mode")
    digest, _ = c.served_nar_digest(c.parse_narinfo(ni))
    check(digest == state["eager_digest"], "drain-mode NAR content identical (served from chunks)")

    # Stop ncps before mutating chunks (force-reclaim is drain-only, exclusive).
    deployment.stop()
    rc, dry = deployment.run_subcommand("migrate-chunks-to-nar", ["--dry-run"])
    check(rc == 0, f"migrate-chunks-to-nar --dry-run exited cleanly (rc={rc})")
    m = re.search(r'total["\s:=]+(\d+)', dry, re.IGNORECASE)
    candidates = int(m.group(1)) if m else 0
    check(candidates > 0, f"dry-run reports drain candidates (total={candidates})")
    check(_chunked_nar_count(db) > 0, "dry-run did not mutate; chunks still present")
    rc, out = deployment.run_subcommand("migrate-chunks-to-nar", ["--force-reclaim"])
    check(rc == 0, f"migrate-chunks-to-nar --force-reclaim exited cleanly (rc={rc})")
    check(_chunked_nar_count(db) == 0, "migrate-chunks-to-nar drained all chunked NARs")

    # PHASE 4 — restart: initCDCDrainMode auto-completion.
    section("PHASE 4 — restart auto-completion")
    deployment.start(cdc=False)
    c = deployment.client(0)
    check(_cdc_config_keys_present(db) == [], "initCDCDrainMode cleared cdc_* config keys")
    deadline = time.time() + 30
    saw_drain = False
    while time.time() < deadline:
        if "drain" in deployment.logs(0).lower():
            saw_drain = True
            break
        time.sleep(1)
    check(saw_drain, "boot log mentions drain completion")
    ni = c.fetch_narinfo(state["eager_hash"])
    check(ni is not None, "drained NAR serves as whole file after restart")
    digest, _ = c.served_nar_digest(c.parse_narinfo(ni))
    check(digest == state["eager_digest"], "post-drain NAR content identical to eager-CDC original")

    # CROSS-CUTTING — upload presence (HEAD==GET) + fsck repair-not-delete.
    section("CROSS-CUTTING — presence + fsck repair")
    for label, key in (("baseline", "baseline_hash"), ("eager", "eager_hash"), ("lazy", "lazy_hash")):
        ni = c.fetch_narinfo(state[key])
        check(ni is not None, f"{label} narinfo present for presence check")
        fields = c.parse_narinfo(ni)
        nar_path = "/" + fields["URL"].lstrip("/")
        head_status, _ = c.head(nar_path)
        get_status, _, body = c.get(nar_path)
        check(
            head_status == get_status == 200 and len(body) > 0,
            f"{label} NAR HEAD/GET agree (both 200, bytes present)",
        )
    before = _total_nar_count(db)
    rc, _ = deployment.run_subcommand("fsck", ["--repair"])
    check(rc == 0, f"fsck --repair exited cleanly (rc={rc})")
    after = _total_nar_count(db)
    log(f"  fsck --repair: nar_files {before} -> {after} (orphan reclaim is OK)", B)
    for label, hash_key, digest_key in (
        ("baseline", "baseline_hash", "baseline_digest"),
        ("eager", "eager_hash", "eager_digest"),
        ("lazy", "lazy_hash", "lazy_digest"),
    ):
        ni = c.fetch_narinfo(state[hash_key])
        check(ni is not None, f"{label} narinfo still served after fsck --repair")
        digest, _ = c.served_nar_digest(c.parse_narinfo(ni))
        check(digest == state[digest_key], f"{label} NAR content unchanged after fsck --repair")
