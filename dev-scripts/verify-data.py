#!/usr/bin/env python3
"""verify-data.py — Verify NCPS data integrity by cross-checking the database
against actual stored files (local filesystem or S3/MinIO).

Reads configuration from var/ncps/state.json written by run.py, so no flags
are needed for connection settings or CDC mode.
"""

import argparse
import hashlib
import io
import json
import os
import subprocess
import sys
from urllib.parse import urlparse

import zstandard as zstd
from blake3 import blake3

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_STATE_FILE = os.path.join(REPO_ROOT, "var", "ncps", "state.json")


# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------


def parse_args():
    parser = argparse.ArgumentParser(
        description="Verify NCPS data integrity. Reads config from state.json (written by run.py)."
    )
    parser.add_argument(
        "--state-file",
        default=DEFAULT_STATE_FILE,
        help=f"Path to state.json (default: {DEFAULT_STATE_FILE})",
    )
    parser.add_argument(
        "--hash",
        dest="filter_hash",
        metavar="HASH",
        help="Verify only the narinfo with this hash",
    )
    parser.add_argument(
        "--limit",
        type=int,
        metavar="N",
        help="Verify at most N narinfos",
    )
    return parser.parse_args()


# ---------------------------------------------------------------------------
# State file loading
# ---------------------------------------------------------------------------


def load_state(state_file):
    try:
        with open(state_file) as f:
            return json.load(f)
    except FileNotFoundError:
        print(f"Error: state file not found: {state_file}")
        print("Is run.py currently running? It writes state.json on startup.")
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"Error: could not parse state file: {e}")
        sys.exit(1)


# ---------------------------------------------------------------------------
# Database connectivity
# ---------------------------------------------------------------------------


def connect_db(state):
    """Return (connection, db_type) where db_type is 'sqlite'/'postgres'/'mysql'."""
    db = state["db"]
    url = state["db_url"]

    if db == "sqlite":
        import sqlite3

        path = url.replace("sqlite:", "", 1)
        conn = sqlite3.connect(path)
        conn.row_factory = sqlite3.Row
        return conn, "sqlite"

    if db == "postgres":
        import psycopg2
        import psycopg2.extras

        conn = psycopg2.connect(url)
        return conn, "postgres"

    if db == "mysql":
        import pymysql
        import pymysql.cursors

        parsed = urlparse(url.replace("mysql://", "http://", 1))
        conn = pymysql.connect(
            host=parsed.hostname,
            port=parsed.port or 3306,
            user=parsed.username,
            password=parsed.password,
            database=parsed.path.lstrip("/"),
            cursorclass=pymysql.cursors.DictCursor,
        )
        return conn, "mysql"

    print(f"Error: unknown db type '{db}'")
    sys.exit(1)


def cursor(conn, db_type):
    """Return a cursor appropriate for the db type."""
    if db_type == "postgres":
        import psycopg2.extras

        return conn.cursor(cursor_factory=psycopg2.extras.RealDictCursor)
    # sqlite Row and pymysql DictCursor both support dict-style access
    return conn.cursor()


def placeholder(db_type):
    """Return the query placeholder character for the db type."""
    return "?" if db_type in ("sqlite", "mysql") else "%s"


# ---------------------------------------------------------------------------
# S3 client
# ---------------------------------------------------------------------------


def get_s3_client(s3_cfg):
    import boto3

    return boto3.client(
        "s3",
        endpoint_url=s3_cfg["endpoint"],
        aws_access_key_id=s3_cfg["access_key"],
        aws_secret_access_key=s3_cfg["secret_key"],
        region_name=s3_cfg["region"],
    )


# ---------------------------------------------------------------------------
# Path helpers (mirroring NCPS two-level sharding)
# ---------------------------------------------------------------------------


def nar_file_key(hash_, compression):
    """Return the relative path/S3 key for a flat NAR file."""
    filename = f"{hash_}.nar"
    if compression and compression not in ("", "none"):
        filename += f".{compression}"
    return os.path.join("store", "nar", hash_[0], hash_[:2], filename)


def chunk_key(hash_):
    """Return the relative path/S3 key for a CDC chunk."""
    return os.path.join("store", "chunk", hash_[0], hash_[:2], hash_)


# ---------------------------------------------------------------------------
# Low-level read helpers
# ---------------------------------------------------------------------------


def read_local(storage_path, rel_key):
    """Read a file from local storage; return bytes or raise."""
    full = os.path.join(storage_path, rel_key)
    if not os.path.exists(full):
        raise FileNotFoundError(f"Missing file: {full}")
    with open(full, "rb") as f:
        return f.read()


def size_local(storage_path, rel_key):
    """Return on-disk byte size for a local file."""
    full = os.path.join(storage_path, rel_key)
    if not os.path.exists(full):
        raise FileNotFoundError(f"Missing file: {full}")
    return os.stat(full).st_size


def read_s3(s3, bucket, rel_key):
    """Read an object from S3; return bytes or raise."""
    # S3 keys use forward slashes regardless of OS
    key = rel_key.replace(os.sep, "/")
    try:
        obj = s3.get_object(Bucket=bucket, Key=key)
        return obj["Body"].read()
    except Exception as e:
        raise FileNotFoundError(f"S3 object not found: s3://{bucket}/{key} ({e})")


def size_s3(s3, bucket, rel_key):
    """Return the ContentLength of an S3 object."""
    key = rel_key.replace(os.sep, "/")
    try:
        obj = s3.head_object(Bucket=bucket, Key=key)
        return obj["ContentLength"]
    except Exception as e:
        raise FileNotFoundError(f"S3 object not found: s3://{bucket}/{key} ({e})")


# ---------------------------------------------------------------------------
# Hash utilities
# ---------------------------------------------------------------------------


def to_nix32(hex_hash):
    """Convert a hex SHA-256 hash to Nix base-32 via nix-hash."""
    try:
        result = subprocess.run(
            ["nix-hash", "--type", "sha256", "--to-base32", hex_hash],
            capture_output=True,
            text=True,
            check=True,
        )
        return result.stdout.strip()
    except (FileNotFoundError, subprocess.CalledProcessError):
        return None


def strip_prefix(h):
    """Strip 'sha256:' prefix from a DB hash value."""
    if h and h.startswith("sha256:"):
        return h[7:]
    return h


def hash_matches(computed_hex, expected_db):
    """Return True if computed_hex (hex SHA-256) matches expected_db (nix32 or hex)."""
    expected = strip_prefix(expected_db)
    if not expected:
        return False
    if computed_hex == expected:
        return True
    nix32 = to_nix32(computed_hex)
    return nix32 is not None and nix32 == expected


# ---------------------------------------------------------------------------
# CDC verification
# ---------------------------------------------------------------------------


def verify_cdc(cur, db_type, state, nar_file_id, nar_hash_db, nar_size_db):
    """
    Reconstruct the NAR from its CDC chunks, verify BLAKE3 per chunk and
    SHA-256 of the full reconstructed NAR against nar_hash_db.

    Returns list of error strings (empty = pass).
    """
    p = placeholder(db_type)
    cur.execute(
        f"""
        SELECT nfc.chunk_index, c.hash, c.size, c.compressed_size
        FROM nar_file_chunks nfc
        JOIN chunks c ON nfc.chunk_id = c.id
        WHERE nfc.nar_file_id = {p}
        ORDER BY nfc.chunk_index
        """,
        (nar_file_id,),
    )
    chunks = cur.fetchall()
    if not chunks:
        return ["CDC: no chunks found in nar_file_chunks (expected > 0)"]

    errors = []
    sha256 = hashlib.sha256()
    total_size = 0
    dctx = zstd.ZstdDecompressor()

    storage = state["storage"]
    storage_path = state.get("storage_path", "")
    s3 = get_s3_client(state["s3"]) if storage == "s3" else None
    bucket = state["s3"]["bucket"] if storage == "s3" else ""

    for chunk in chunks:
        chunk_hash = chunk["hash"]
        expected_compressed_size = chunk["compressed_size"]
        expected_size = chunk["size"]
        key = chunk_key(chunk_hash)

        # --- read compressed bytes ---
        try:
            if storage == "local":
                compressed = read_local(storage_path, key)
            else:
                compressed = read_s3(s3, bucket, key)
        except FileNotFoundError as e:
            errors.append(f"Chunk {chunk_hash[:12]}…: {e}")
            continue

        # --- verify compressed size ---
        if len(compressed) != expected_compressed_size:
            errors.append(
                f"Chunk {chunk_hash[:12]}…: compressed size mismatch "
                f"(disk {len(compressed)}, DB {expected_compressed_size})"
            )

        # --- decompress ---
        try:
            data = dctx.decompress(compressed)
        except zstd.ZstdError as e:
            errors.append(f"Chunk {chunk_hash[:12]}…: zstd decompression failed: {e}")
            continue

        # --- verify uncompressed size ---
        if len(data) != expected_size:
            errors.append(
                f"Chunk {chunk_hash[:12]}…: uncompressed size mismatch "
                f"(got {len(data)}, DB {expected_size})"
            )

        # --- verify BLAKE3 hash ---
        computed_b3 = blake3(data).hexdigest()
        if computed_b3 != chunk_hash:
            errors.append(
                f"Chunk {chunk_hash[:12]}…: BLAKE3 mismatch "
                f"(got {computed_b3[:12]}…, expected {chunk_hash[:12]}…)"
            )

        sha256.update(data)
        total_size += len(data)

    if errors:
        return errors

    # --- verify reconstructed NAR size ---
    if total_size != nar_size_db:
        errors.append(
            f"Reconstructed NAR size mismatch (got {total_size}, DB nar_size={nar_size_db})"
        )

    # --- verify reconstructed NAR hash ---
    if not hash_matches(sha256.hexdigest(), nar_hash_db):
        nix32 = to_nix32(sha256.hexdigest())
        errors.append(
            f"Reconstructed NAR hash mismatch\n"
            f"  Expected (DB): {strip_prefix(nar_hash_db)}\n"
            f"  Got (hex):     {sha256.hexdigest()}\n"
            f"  Got (nix32):   {nix32}"
        )

    return errors


# ---------------------------------------------------------------------------
# Flat-file verification
# ---------------------------------------------------------------------------


def verify_flat(state, nar_file_hash, compression, file_size_db, file_hash_db):
    """
    Read the compressed NAR file from disk or S3, verify its size and SHA-256
    against file_size_db and file_hash_db.

    Returns list of error strings (empty = pass).
    """
    errors = []
    key = nar_file_key(nar_file_hash, compression)

    storage = state["storage"]
    storage_path = state.get("storage_path", "")
    s3 = get_s3_client(state["s3"]) if storage == "s3" else None
    bucket = state["s3"]["bucket"] if storage == "s3" else ""

    # --- read raw (compressed) bytes ---
    try:
        if storage == "local":
            raw = read_local(storage_path, key)
        else:
            raw = read_s3(s3, bucket, key)
    except FileNotFoundError as e:
        return [str(e)]

    # --- verify physical size ---
    if len(raw) != file_size_db:
        errors.append(
            f"File size mismatch (disk {len(raw)}, DB file_size={file_size_db})"
        )

    # --- verify SHA-256 of compressed file ---
    sha256_hex = hashlib.sha256(raw).hexdigest()
    if not hash_matches(sha256_hex, file_hash_db):
        nix32 = to_nix32(sha256_hex)
        errors.append(
            f"File hash mismatch\n"
            f"  Expected (DB): {strip_prefix(file_hash_db)}\n"
            f"  Got (hex):     {sha256_hex}\n"
            f"  Got (nix32):   {nix32}"
        )

    return errors


# ---------------------------------------------------------------------------
# Main verification loop
# ---------------------------------------------------------------------------


def main():
    args = parse_args()
    state = load_state(args.state_file)

    cdc_enabled = state.get("cdc", False)
    storage = state.get("storage", "local")
    mode_label = f"{'CDC' if cdc_enabled else 'non-CDC'}, {state.get('db', '?')} DB, {storage} storage"

    print(f"ncps verify-data  [{mode_label}]")
    print(f"State file: {args.state_file}")
    print()

    conn, db_type = connect_db(state)
    cur = cursor(conn, db_type)
    p = placeholder(db_type)

    try:
        # --- fetch narinfos ---
        if args.filter_hash:
            cur.execute(
                f"SELECT * FROM narinfos WHERE hash = {p}",
                (args.filter_hash,),
            )
        else:
            cur.execute("SELECT * FROM narinfos ORDER BY id")

        narinfos = cur.fetchall()

        if args.limit:
            narinfos = narinfos[: args.limit]

        total = len(narinfos)
        print(f"Verifying {total} narinfo(s)...\n")

        failures = 0

        for ni in narinfos:
            ni_id = ni["id"]
            ni_hash = ni["hash"]
            store_path = ni["store_path"] or "(unknown)"

            print(f"[{ni_hash}]  {store_path}")

            # --- find linked nar_file ---
            cur.execute(
                f"""
                SELECT nf.*
                FROM nar_files nf
                JOIN narinfo_nar_files nnf ON nnf.nar_file_id = nf.id
                WHERE nnf.narinfo_id = {p}
                """,
                (ni_id,),
            )
            nar_files = cur.fetchall()

            if not nar_files:
                print("  [FAIL] No linked nar_file found in narinfo_nar_files")
                failures += 1
                print()
                continue

            for nf in nar_files:
                nf_id = nf["id"]
                total_chunks = nf["total_chunks"]
                nf_hash = nf["hash"]
                nf_compression = nf["compression"] or ""
                nf_file_size = nf["file_size"]

                if cdc_enabled:
                    # Expect chunks; absence is a failure
                    if total_chunks == 0:
                        print(
                            f"  [FAIL] CDC enabled but nar_file {nf_id} has total_chunks=0 "
                            f"(no chunks stored)"
                        )
                        failures += 1
                        continue

                    errs = verify_cdc(
                        cur,
                        db_type,
                        state,
                        nf_id,
                        ni["nar_hash"],
                        ni["nar_size"],
                    )
                    if errs:
                        for e in errs:
                            print(f"  [FAIL] {e}")
                        failures += 1
                    else:
                        print(
                            f"  [PASS] CDC: {total_chunks} chunk(s) verified, "
                            f"NAR hash and size match"
                        )

                else:
                    # Expect flat file; presence of chunks is a failure
                    if total_chunks > 0:
                        print(
                            f"  [FAIL] CDC disabled but nar_file {nf_id} has "
                            f"total_chunks={total_chunks} (unexpected chunks)"
                        )
                        failures += 1
                        continue

                    errs = verify_flat(
                        state,
                        nf_hash,
                        nf_compression,
                        nf_file_size,
                        ni["file_hash"],
                    )
                    if errs:
                        for e in errs:
                            print(f"  [FAIL] {e}")
                        failures += 1
                    else:
                        desc = (
                            f"{nf_compression}"
                            if nf_compression and nf_compression != "none"
                            else "uncompressed"
                        )
                        print(f"  [PASS] Flat file ({desc}): size and hash match")

            print()

        # --- summary ---
        if failures == 0:
            print(f"SUCCESS: all {total} narinfo(s) passed verification.")
        else:
            print(f"FAILURE: {failures} error(s) found across {total} narinfo(s).")
            sys.exit(1)

    finally:
        conn.close()


if __name__ == "__main__":
    main()
