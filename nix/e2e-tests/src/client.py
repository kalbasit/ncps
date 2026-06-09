"""HTTP client + NAR helpers against an ncps replica.

Bound to one replica base URL so the same code serves both modes (local
fixed ports, kubernetes port-forwards). Lifted from the former drivers'
narinfo/NAR fetch, decompress, and byte-compare helpers.
"""

from __future__ import annotations

import hashlib
import io
import json
import lzma
import os
import subprocess
import sys
import urllib.error
import urllib.request
from typing import Dict, Optional, Tuple

from harness_config import REPO_ROOT, VAR_NCPS


class Client:
    """Talks to a single ncps replica at ``base_url``."""

    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")

    # -- raw HTTP --------------------------------------------------------------

    def get(self, path: str, timeout: int = 300) -> Tuple[int, Dict[str, str], bytes]:
        url = self.base_url + "/" + path.lstrip("/")
        with urllib.request.urlopen(url, timeout=timeout) as r:
            return r.status, dict(r.headers), r.read()

    def head(self, path: str, timeout: int = 30) -> Tuple[int, Dict[str, str]]:
        url = self.base_url + "/" + path.lstrip("/")
        req = urllib.request.Request(url, method="HEAD")
        try:
            with urllib.request.urlopen(req, timeout=timeout) as r:
                return r.status, dict(r.headers)
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers)

    def pubkey(self) -> str:
        _, _, body = self.get("/pubkey")
        return body.decode("utf-8").strip()

    # -- narinfo / NAR ---------------------------------------------------------

    def fetch_narinfo(self, store_hash: str) -> Optional[str]:
        """Raw .narinfo text for a store-path hash, or None on 404."""
        try:
            status, _, body = self.get(f"/{store_hash}.narinfo")
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            raise
        return body.decode("utf-8") if status == 200 else None

    @staticmethod
    def parse_narinfo(text: str) -> Dict[str, str]:
        fields: Dict[str, str] = {}
        for line in text.splitlines():
            if ":" in line:
                k, v = line.split(":", 1)
                fields[k.strip()] = v.strip()
        return fields

    def fetch_nar_bytes(self, narinfo_fields: Dict[str, str]) -> bytes:
        url = narinfo_fields["URL"]
        status, _, body = self.get("/" + url.lstrip("/"))
        if status != 200:
            raise RuntimeError(f"fetch_nar_bytes: status {status} for {url}")
        return body

    def served_nar_digest(self, narinfo_fields: Dict[str, str]) -> Tuple[str, int]:
        """(sha256 of DECOMPRESSED NAR, served byte length).

        Comparing the decompressed-content digest proves byte-identity across
        compression representations (xz whole-file vs none-from-chunks), which
        catches same-size corruption a length check would miss.
        """
        raw = self.fetch_nar_bytes(narinfo_fields)
        comp = narinfo_fields.get("Compression", "none")
        if comp in ("none", ""):
            data = raw
        elif comp == "xz":
            data = lzma.decompress(raw)
        elif comp in ("zst", "zstd"):
            import zstandard

            data = zstandard.ZstdDecompressor().stream_reader(io.BytesIO(raw)).read()
        else:
            raise RuntimeError(f"served_nar_digest: unsupported compression {comp!r}")
        return hashlib.sha256(data).hexdigest(), len(raw)


def decode_nar(raw: bytes, comp: str) -> bytes:
    """Decompressed NAR bytes for a narinfo Compression value."""
    if comp in ("none", ""):
        return raw
    if comp == "xz":
        return lzma.decompress(raw)
    if comp in ("zst", "zstd"):
        import zstandard

        return zstandard.ZstdDecompressor().stream_reader(io.BytesIO(raw)).read()
    raise RuntimeError(f"decode_nar: unsupported compression {comp!r}")


# -- canonical NAR + cache seeding (mode-independent) --------------------------


def realise_package(package: str) -> str:
    """Realise a package on the HOST store and return its out path.

    Needed so ``nix-store --dump`` can produce the canonical NAR bytes locally;
    ncps then serves the same path by pulling it from upstream on demand. (The
    isolated :func:`seed_cache` build runs in a throwaway store, so its output
    is not dumpable on the host — that path is for pushing NEW NARs into ncps.)
    """
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


def canonical_nar_sha256(store_path: str) -> str:
    """sha256 of `nix-store --dump <path>` — the canonical NAR serialization."""
    raw = subprocess.check_output(
        ["nix-store", "--dump", store_path], cwd=REPO_ROOT, timeout=300
    )
    return hashlib.sha256(raw).hexdigest()


def hash_of_store_path(store_path: str) -> str:
    """The 32-char store-path hash (the narinfo key ncps serves under)."""
    return os.path.basename(store_path).split("-", 1)[0]


def store_hash_of(flakeref: str) -> str:
    """Resolve a flakeref to the 32-char store-path hash of its output."""
    out = subprocess.check_output(
        ["nix", "path-info", "--json", flakeref], cwd=REPO_ROOT, text=True, timeout=120
    )
    data = json.loads(out)
    path = data[0]["path"] if isinstance(data, list) else next(iter(data))
    return os.path.basename(path).split("-", 1)[0]


def seed_cache(packages) -> None:
    """Build packages through ncps via dev-scripts/nix-isolated-build.py."""
    cmd = [
        sys.executable,
        os.path.join(REPO_ROOT, "dev-scripts", "nix-isolated-build.py"),
        *packages,
    ]
    nix_tmp = os.path.join(VAR_NCPS, "nix-tmp")
    os.makedirs(nix_tmp, exist_ok=True)
    env = os.environ.copy()
    env["TMPDIR"] = nix_tmp
    r = subprocess.run(cmd, cwd=REPO_ROOT, timeout=900, env=env)
    if r.returncode != 0:
        raise RuntimeError(f"seed_cache failed (exit {r.returncode})")
