#!/usr/bin/env python3
"""Unit tests for the HA round-robin reverse proxy in run.py.

These exercise the proxy in isolation against lightweight stub backends, so no
ncps instances, databases, or storage are required.

Run with: python3 -m unittest dev-scripts/test_run_proxy.py  (from repo root)
or:       python3 -m unittest test_run_proxy                 (from dev-scripts/)
"""

import http.client
import http.server
import json
import os
import socket
import sys
import threading
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import run  # noqa: E402 — sys.path tweak must precede the import


def _make_stub_handler():
    class StubHandler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def _respond(self, body):
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("X-Backend", str(self.server.backend_id))
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            if self.command != "HEAD":
                self.wfile.write(body)

        def do_GET(self):
            self._respond(
                f"backend={self.server.backend_id} path={self.path}".encode()
            )

        def do_HEAD(self):
            self._respond(b"x" * 123)

        def do_PUT(self):
            length = int(self.headers.get("Content-Length", 0))
            data = self.rfile.read(length)
            self._respond(
                f"backend={self.server.backend_id} putlen={len(data)}".encode()
            )

    return StubHandler


def _start_stub(backend_id):
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _make_stub_handler())
    server.backend_id = backend_id
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def _addr(server):
    host, port = server.server_address[:2]
    return f"{host}:{port}"


def _free_port():
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def _request(proxy, method, path, body=None, headers=None):
    conn = http.client.HTTPConnection("127.0.0.1", proxy.server_address[1], timeout=5)
    conn.request(method, path, body=body, headers=headers or {})
    resp = conn.getresponse()
    data = resp.read()
    status, hdrs = resp.status, dict(resp.getheaders())
    conn.close()
    return status, hdrs, data


class TestPickBackend(unittest.TestCase):
    def test_round_robin_and_wraparound(self):
        backends = ["a", "b", "c"]
        picked = [run.pick_backend(backends, i) for i in range(7)]
        self.assertEqual(picked, ["a", "b", "c", "a", "b", "c", "a"])


class TestProxyForwarding(unittest.TestCase):
    def setUp(self):
        self.stubs = []
        self.proxies = []

    def tearDown(self):
        for s in self.proxies + self.stubs:
            s.shutdown()
            s.server_close()

    def _proxy(self, backends):
        proxy = run.start_proxy("127.0.0.1", 0, backends)
        self.proxies.append(proxy)
        return proxy

    def test_get_preserves_status_headers_body(self):
        stub = _start_stub("s0")
        self.stubs.append(stub)
        proxy = self._proxy([_addr(stub)])

        status, hdrs, data = _request(proxy, "GET", "/nix-cache-info")

        self.assertEqual(status, 200)
        self.assertEqual(hdrs.get("X-Backend"), "s0")
        self.assertIn(b"backend=s0", data)
        self.assertIn(b"path=/nix-cache-info", data)

    def test_requests_rotate_across_backends(self):
        stub0 = _start_stub("s0")
        stub1 = _start_stub("s1")
        self.stubs.extend([stub0, stub1])
        proxy = self._proxy([_addr(stub0), _addr(stub1)])

        seen = [_request(proxy, "GET", "/")[1]["X-Backend"] for _ in range(4)]
        self.assertEqual(seen, ["s0", "s1", "s0", "s1"])

    def test_put_body_is_forwarded(self):
        stub = _start_stub("s0")
        self.stubs.append(stub)
        proxy = self._proxy([_addr(stub)])

        payload = b"y" * 5000
        status, _hdrs, data = _request(
            proxy,
            "PUT",
            "/upload",
            body=payload,
            headers={"Content-Length": str(len(payload))},
        )

        self.assertEqual(status, 200)
        self.assertIn(f"putlen={len(payload)}".encode(), data)

    def test_dead_backend_returns_502(self):
        dead = f"127.0.0.1:{_free_port()}"  # nothing is listening here
        proxy = self._proxy([dead])

        status, _hdrs, _data = _request(proxy, "GET", "/")
        self.assertEqual(status, 502)


class TestProxyLifecycle(unittest.TestCase):
    def test_shutdown_releases_port(self):
        stub = _start_stub("s0")
        proxy = run.start_proxy("127.0.0.1", 0, [_addr(stub)])
        port = proxy.server_address[1]

        # Port is open while the proxy runs.
        probe = socket.create_connection(("127.0.0.1", port), timeout=5)
        probe.close()

        proxy.shutdown()
        proxy.server_close()
        stub.shutdown()
        stub.server_close()

        # Port is released after shutdown.
        with self.assertRaises(ConnectionRefusedError):
            socket.create_connection(("127.0.0.1", port), timeout=5)


class TestStateFileProxyField(unittest.TestCase):
    def setUp(self):
        self.state_path = os.path.join(run.REPO_ROOT, "var/ncps/state.json")
        self._existed = os.path.exists(self.state_path)
        self._backup = None
        if self._existed:
            with open(self.state_path) as f:
                self._backup = f.read()

    def tearDown(self):
        # Restore any pre-existing state file (e.g. a live run.py session).
        if self._backup is not None:
            with open(self.state_path, "w") as f:
                f.write(self._backup)
        elif os.path.exists(self.state_path):
            os.remove(self.state_path)

    def _write_and_read(self, config):
        path = run.write_state_file([{"port": 8501, "pid": 1}], config)
        with open(path) as f:
            return json.load(f)

    def test_proxy_advertised_when_present(self):
        endpoint = {"host": "127.0.0.1", "port": run.PROXY_PORT}
        data = self._write_and_read({"locker": "redis", "proxy": endpoint})
        self.assertEqual(data.get("proxy"), endpoint)

    def test_proxy_omitted_when_absent(self):
        data = self._write_and_read({"locker": "local"})
        self.assertNotIn("proxy", data)


if __name__ == "__main__":
    unittest.main()
