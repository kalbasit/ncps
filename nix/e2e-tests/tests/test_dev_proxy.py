"""Regression tests for the dev HA round-robin reverse proxy in
``dev-scripts/run.py``.

These drive the *actual* proxy code (`make_proxy_handler`/`start_proxy`) in
front of an in-test capture backend that records how many request-body bytes it
actually receives. They guard the bug where a `Transfer-Encoding: chunked`
upload had its body silently dropped (backend received 0 bytes), which `nix
copy` saw as ``curl error 56: Connection reset by peer`` on large NAR uploads.
"""

from __future__ import annotations

import http.client
import http.server
import importlib.util
import os
import socket
import threading
import time
from pathlib import Path
from typing import ClassVar

import pytest


def _load_run_py():
    """Import dev-scripts/run.py as a module.

    Resolves the path from ``NCPS_RUN_PY`` if set, else walks up from this test
    file looking for ``dev-scripts/run.py`` (works both in-repo and inside the
    nix flake-check sandbox where dev-scripts is copied next to tests/)."""
    override = os.environ.get("NCPS_RUN_PY")
    candidates = []
    if override:
        candidates.append(Path(override))
    here = Path(__file__).resolve()
    for parent in [here.parent, *here.parents]:
        candidates.append(parent / "dev-scripts" / "run.py")
    run_py = next((p for p in candidates if p.is_file()), None)
    if run_py is None:
        pytest.skip("could not locate dev-scripts/run.py")
    spec = importlib.util.spec_from_file_location("ncps_run_py", run_py)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


run = _load_run_py()


def _free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


@pytest.fixture(autouse=True)
def _clear_capture_state():
    """Reset the shared capture dict before each test so per-test body-length
    records never leak across tests in the same process."""
    _CaptureHandler.received.clear()
    yield


class _CaptureHandler(http.server.BaseHTTPRequestHandler):
    """Backend that records the request-body length per X-Test-Case."""

    received: ClassVar[dict] = {}

    def log_message(self, *_args):
        pass

    def do_PUT(self):
        cl = self.headers.get("Content-Length")
        te = self.headers.get("Transfer-Encoding")
        body = b""
        if cl is not None:
            body = self.rfile.read(int(cl))
        elif te and "chunked" in te.lower():
            while True:
                line = self.rfile.readline().strip()
                if not line:
                    break
                size = int(line.split(b";", 1)[0], 16)
                if size == 0:
                    self.rfile.readline()  # trailing CRLF after terminator
                    break
                body += self.rfile.read(size)
                self.rfile.readline()  # CRLF after each chunk
        type(self).received[self.headers.get("X-Test-Case")] = len(body)
        self.send_response(204)
        self.end_headers()


def _start_capture_backend():
    port = _free_port()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", port), _CaptureHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, port


def _put(proxy_port, case, body, *, chunked: bool):
    conn = http.client.HTTPConnection("127.0.0.1", proxy_port, timeout=10)
    try:
        path = f"/upload/nar/{case}.nar.zst"
        if chunked:
            conn.putrequest("PUT", path, skip_accept_encoding=True)
            conn.putheader("X-Test-Case", case)
            conn.putheader("Transfer-Encoding", "chunked")
            conn.endheaders()
            conn.send(b"%X\r\n" % len(body) + body + b"\r\n0\r\n\r\n")
        else:
            conn.request(
                "PUT",
                path,
                body=body,
                headers={"X-Test-Case": case, "Content-Length": str(len(body))},
            )
        resp = conn.getresponse()
        resp.read()
        return resp.status
    finally:
        conn.close()


PAYLOAD = b"X" * 200_000


def test_content_length_put_forwards_full_body():
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        status = _put(proxy_port, "cl_case", PAYLOAD, chunked=False)
        time.sleep(0.1)
        assert status == 204
        assert _CaptureHandler.received.get("cl_case") == len(PAYLOAD)
    finally:
        proxy.shutdown()
        backend.shutdown()


def test_chunked_put_forwards_full_body():
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        status = _put(proxy_port, "chunked_case", PAYLOAD, chunked=True)
        time.sleep(0.1)
        assert status == 204
        # The bug: backend received 0 bytes because the proxy dropped the
        # chunked body. The full payload must reach the backend.
        assert _CaptureHandler.received.get("chunked_case") == len(PAYLOAD)
    finally:
        proxy.shutdown()
        backend.shutdown()


@pytest.mark.parametrize("chunked", [False, True])
def test_unreachable_backend_returns_502_not_reset(chunked):
    # Point the proxy at a port with nothing listening.
    dead_port = _free_port()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{dead_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        # The client must observe a clean 502, not a ConnectionResetError, for
        # both Content-Length and Transfer-Encoding: chunked uploads.
        status = _put(proxy_port, "dead_case", b"Y" * 4096, chunked=chunked)
        assert status == 502
    finally:
        proxy.shutdown()


def test_truncated_content_length_body_returns_502_not_hang():
    # A client that promises a Content-Length but closes early must not leave
    # the proxy hanging in conn.getresponse() waiting on a backend that still
    # expects the full body — it must surface a 502.
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=10)
        try:
            # Declare 200000 bytes but send only 10, then half-close.
            sock.sendall(
                b"PUT /upload/nar/truncated.nar.zst HTTP/1.1\r\n"
                b"Host: 127.0.0.1\r\n"
                b"Content-Length: 200000\r\n"
                b"\r\n"
                b"XXXXXXXXXX"
            )
            sock.shutdown(socket.SHUT_WR)
            # Read the status line; timeout (not hang) if the fix regresses.
            sock.settimeout(10)
            status_line = sock.recv(64)
            assert status_line.startswith(b"HTTP/"), status_line
            assert b" 502 " in status_line, status_line
        finally:
            sock.close()
    finally:
        proxy.shutdown()
        backend.shutdown()
