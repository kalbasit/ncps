"""Database access for invariant assertions.

Keeps a per-dialect query path (sqlite/postgres/mysql) so phase drivers can
assert ``nar_file`` / ``config`` rows directly. Lifted from the former drivers'
``db_query``; the ``?`` placeholder is rewritten per dialect.
"""

from __future__ import annotations

import os
from typing import Any, List, Sequence, Tuple

from harness_config import DB_URLS


class DBAccess:
    """Read-only query helper bound to one database dialect + URL."""

    def __init__(self, dialect: str, url: str | None = None):
        self.dialect = dialect
        self.url = url or DB_URLS[dialect]

    def query(self, sql: str, params: Sequence[Any] = ()) -> List[Tuple]:
        if self.dialect == "sqlite":
            import sqlite3

            path = self.url.split(":", 1)[1]
            if not os.path.exists(path):
                return []
            conn = sqlite3.connect(path)
            try:
                return list(conn.execute(sql, params).fetchall())
            finally:
                conn.close()
        if self.dialect == "postgres":
            import psycopg2

            conn = psycopg2.connect(self.url)
            try:
                with conn.cursor() as cur:
                    cur.execute(sql.replace("?", "%s"), params)
                    return list(cur.fetchall())
            finally:
                conn.close()
        if self.dialect == "mysql":
            import pymysql
            from urllib.parse import urlparse

            parsed = urlparse(self.url)
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
        raise ValueError(f"unknown db dialect: {self.dialect}")

    def scalar(self, sql: str, params: Sequence[Any] = ()):
        rows = self.query(sql, params)
        if not rows:
            return None
        return rows[0][0]
