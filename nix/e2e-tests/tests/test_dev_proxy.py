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

    def do_GET(self):
        # Two response shapes for keep-alive framing tests:
        #   /upload/framed   -> 200 with an explicit Content-Length (delimitable)
        #   /upload/unframed -> 200 with NO Content-Length; this HTTP/1.0 backend
        #                       signals end-of-body by closing the connection.
        if self.path == "/upload/framed":
            body = b"framed-body"
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        elif self.path == "/upload/unframed":
            body = b"unframed-body-no-length"
            self.send_response(200)
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.send_header("Content-Length", "0")
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


def _read_response_head(sock):
    """Read one HTTP response's status line + headers from ``sock``.

    Returns ``(status_line, headers, leftover)`` where ``headers`` is a
    lower-cased dict and ``leftover`` is any bytes already read past the header
    block (the start of the body), or ``(None, data, b"")`` if the connection
    closed before a full header block arrived. Returning ``leftover`` keeps
    body reads deterministic even when the kernel coalesces the headers and
    body into a single ``recv``."""
    data = b""
    while b"\r\n\r\n" not in data:
        chunk = sock.recv(4096)
        if not chunk:
            return None, data, b""
        data += chunk
    head, leftover = data.split(b"\r\n\r\n", 1)
    lines = head.split(b"\r\n")
    headers = {}
    for line in lines[1:]:
        if b":" in line:
            key, value = line.split(b":", 1)
            headers[key.strip().lower()] = value.strip()
    return lines[0], headers, leftover


def _send_put(sock, case, body):
    sock.sendall(
        f"PUT /upload/nar/{case}.nar.zst HTTP/1.1\r\n".encode()
        + b"Host: 127.0.0.1\r\n"
        + f"X-Test-Case: {case}\r\n".encode()
        + f"Content-Length: {len(body)}\r\n".encode()
        + b"Connection: keep-alive\r\n\r\n"
        + body
    )


def test_http11_connection_is_reused_across_requests():
    # The bug: the proxy spoke HTTP/1.0 and closed every connection after one
    # request, so a keep-alive client (nix/libcurl) was forced to open a new
    # TCP connection per object — thousands for a large closure — which under
    # load intermittently reset uploads (curl error 56). The proxy must keep a
    # persistent HTTP/1.1 connection so the client can reuse it.
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=10)
        sock.settimeout(10)
        try:
            for i in range(3):
                _send_put(sock, f"reuse_{i}", b"X" * 1000)
                status, _headers, _leftover = _read_response_head(sock)
                assert status is not None, (
                    f"proxy closed the connection before request {i}; "
                    "it is not honoring keep-alive"
                )
                assert status.startswith(b"HTTP/1.1"), status
                assert b"204" in status, status
            # All three requests were served on a single reused connection.
            assert _CaptureHandler.received.get("reuse_2") == 1000
        finally:
            sock.close()
    finally:
        proxy.shutdown()
        backend.shutdown()


def test_expect_100_continue_is_answered():
    # A large PUT carries `Expect: 100-continue`; the proxy must answer the
    # interim `100 Continue` before the client streams the body, otherwise the
    # client stalls ~1s per upload waiting for it.
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=5)
        sock.settimeout(5)
        try:
            body = b"X" * 5000
            sock.sendall(
                b"PUT /upload/nar/expect_case.nar.zst HTTP/1.1\r\n"
                b"Host: 127.0.0.1\r\n"
                b"X-Test-Case: expect_case\r\n"
                b"Content-Length: 5000\r\n"
                b"Expect: 100-continue\r\n\r\n"
            )
            interim, _headers, _leftover = _read_response_head(sock)
            assert interim is not None and interim.startswith(b"HTTP/1.1 100"), interim
            sock.sendall(body)
            status, _headers2, _leftover2 = _read_response_head(sock)
            assert status is not None and b"204" in status, status
        finally:
            sock.close()
    finally:
        proxy.shutdown()
        backend.shutdown()


def test_client_connection_close_is_echoed():
    # RFC 7230 §6.3: when the proxy will close the connection — e.g. the client
    # asked with `Connection: close` — it MUST advertise `Connection: close` in
    # the response, even for an otherwise-reusable (delimitable) response, so a
    # keep-alive client does not assume the connection persists and reuse it.
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=10)
        sock.settimeout(10)
        try:
            body = b"X" * 100
            sock.sendall(
                b"PUT /upload/nar/close_case.nar.zst HTTP/1.1\r\n"
                b"Host: 127.0.0.1\r\n"
                b"X-Test-Case: close_case\r\n"
                + f"Content-Length: {len(body)}\r\n".encode()
                + b"Connection: close\r\n\r\n"
                + body
            )
            status, headers, _leftover = _read_response_head(sock)
            assert status is not None and b"204" in status, status
            assert headers.get(b"connection", b"").lower() == b"close", headers
        finally:
            sock.close()
    finally:
        proxy.shutdown()
        backend.shutdown()


def test_unframed_response_forces_connection_close():
    # If the backend response has no Content-Length and is not bodiless, the
    # proxy cannot keep the connection in sync for a following request, so it
    # must signal `Connection: close` and let the client read to EOF rather
    # than mis-framing the next response.
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=10)
        sock.settimeout(10)
        try:
            sock.sendall(
                b"GET /upload/unframed HTTP/1.1\r\n"
                b"Host: 127.0.0.1\r\n"
                b"Connection: keep-alive\r\n\r\n"
            )
            status, headers, leftover = _read_response_head(sock)
            assert status is not None and b"200" in status, status
            assert headers.get(b"connection", b"").lower() == b"close", headers
            # The rest of the socket is the body, terminated by EOF (close).
            rest = bytearray(leftover)
            while True:
                chunk = sock.recv(4096)
                if not chunk:
                    break
                rest += chunk
            assert b"unframed-body-no-length" in bytes(rest), bytes(rest)
        finally:
            sock.close()
    finally:
        proxy.shutdown()
        backend.shutdown()


def test_framed_get_keeps_connection_alive():
    # A delimitable response (explicit Content-Length) must keep the connection
    # open AND be framed exactly so a second request on the same raw socket is
    # served correctly. (A buffered client like http.client would silently
    # reconnect on an HTTP/1.0 close and mask the bug, so we use a raw socket.)
    backend, backend_port = _start_capture_backend()
    proxy = run.start_proxy("127.0.0.1", _free_port(), [f"127.0.0.1:{backend_port}"])
    proxy_port = proxy.server_address[1]
    try:
        time.sleep(0.2)
        sock = socket.create_connection(("127.0.0.1", proxy_port), timeout=10)
        sock.settimeout(10)
        try:
            for _ in range(2):
                sock.sendall(
                    b"GET /upload/framed HTTP/1.1\r\n"
                    b"Host: 127.0.0.1\r\n"
                    b"Connection: keep-alive\r\n\r\n"
                )
                status, headers, leftover = _read_response_head(sock)
                assert status is not None, "proxy did not keep the connection alive"
                assert status.startswith(b"HTTP/1.1") and b"200" in status, status
                length = int(headers.get(b"content-length", b"0"))
                body = bytearray(leftover)
                while len(body) < length:
                    chunk = sock.recv(length - len(body))
                    if not chunk:
                        break
                    body += chunk
                assert bytes(body) == b"framed-body", bytes(body)
        finally:
            sock.close()
    finally:
        proxy.shutdown()
        backend.shutdown()
