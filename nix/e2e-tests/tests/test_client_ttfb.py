"""Unit tests for the harness client's time-to-first-byte measurement.

TTFB is the assertion that would have caught the production stall: every NAR in
the failing runs was byte-perfect, but the first byte arrived ~57s late, so the
ingress aborted the response mid-body and the client saw a truncated 200. A
harness that only compares bytes — and waits up to 900s to do it — scores that
as a PASS. These tests pin the measurement itself against a stub server whose
first byte is deliberately late.
"""

from __future__ import annotations

import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest

from client import Client


def _make_server(delay_before_first_byte: float, body: bytes):
    """An HTTP server that stalls `delay` seconds before the first body byte.

    Headers (and the 200) are sent immediately, then the body is withheld. That
    is exactly the production shape: the status line commits, and the stall
    happens afterwards, so anything measuring only the status or the final bytes
    sees nothing wrong.
    """

    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_GET(self):  # noqa: N802 — BaseHTTPRequestHandler's required name
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.flush()

            time.sleep(delay_before_first_byte)

            self.wfile.write(body)
            self.wfile.flush()

        def log_message(self, *_args):
            pass  # keep test output quiet

    server = HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    return server, f"http://127.0.0.1:{server.server_port}"


def test_ttfb_measures_the_stall_not_the_payload():
    body = b"x" * 4096
    server, base = _make_server(1.0, body)

    try:
        resp = Client(base).get_timed("/slow", timeout=30)
    finally:
        server.shutdown()

    assert resp.status == 200
    assert resp.body == body, "the body must still be delivered intact"
    assert resp.ttfb_seconds >= 1.0, "TTFB must include the pre-body stall"
    assert resp.total_seconds >= resp.ttfb_seconds


def test_fast_response_has_small_ttfb():
    body = b"y" * 4096
    server, base = _make_server(0.0, body)

    try:
        resp = Client(base).get_timed("/fast", timeout=30)
    finally:
        server.shutdown()

    assert resp.status == 200
    assert resp.body == body
    assert resp.ttfb_seconds < 1.0, "a healthy response must not be flagged as slow"


def test_byte_correct_but_slow_is_distinguishable_from_fast():
    """The regression guard: identical bytes, different TTFB.

    Both responses are byte-identical, so a bytes-only assertion cannot tell them
    apart. TTFB can, and must.
    """
    body = b"z" * 4096

    slow_server, slow_base = _make_server(1.0, body)
    fast_server, fast_base = _make_server(0.0, body)

    try:
        slow = Client(slow_base).get_timed("/slow", timeout=30)
        fast = Client(fast_base).get_timed("/fast", timeout=30)
    finally:
        slow_server.shutdown()
        fast_server.shutdown()

    assert slow.body == fast.body, "precondition: the payloads are identical"
    assert slow.ttfb_seconds > fast.ttfb_seconds + 0.5, (
        "a byte-correct but slow response must be distinguishable from a fast one; "
        "this is the distinction the production stall hid from every existing scenario"
    )


@pytest.mark.parametrize("budget", [0.25])
def test_budget_violation_is_detectable(budget):
    """A declared budget must be able to fail a byte-correct response."""
    body = b"w" * 1024
    server, base = _make_server(1.0, body)

    try:
        resp = Client(base).get_timed("/slow", timeout=30)
    finally:
        server.shutdown()

    assert resp.status == 200
    assert resp.body == body
    assert resp.ttfb_seconds > budget, (
        "the stub stalls 1s against a 0.25s budget, so this must register as a violation"
    )
