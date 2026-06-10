## 1. Regression test (TDD red)

- [x] 1.1 Add a test (sibling of `nix/e2e-tests/tests/test_dev_proxy.py`, importing the real proxy via `NCPS_RUN_PY`) asserting the proxy answers `HTTP/1.1` and a single client connection can serve multiple sequential requests (PUT/HEAD) without a reset. Confirm it FAILS against the current HTTP/1.0 proxy (the second request on a reused connection resets).
- [x] 1.2 Add a test asserting forwarded responses stay correctly framed on a reused connection: a `Content-Length`/`204` response keeps the connection alive; a response with unknown body length is delimited via `Connection: close`.
- [x] 1.3 Add a test asserting an `Expect: 100-continue` request through the proxy receives an interim `100 Continue` before the body.

## 2. Fix the proxy (TDD green) — `dev-scripts/run.py` only

- [x] 2.1 Set `ProxyHandler.protocol_version = "HTTP/1.1"` in `make_proxy_handler` so client connections are persistent and `Expect: 100-continue` is auto-answered.
- [x] 2.2 In `_proxy`'s response-forwarding, frame the response for safe reuse: track whether a `Content-Length` was forwarded; treat `204`/`304`/`HEAD`/`1xx` as bodiless; when the body length is unknown (e.g. backend `Transfer-Encoding: chunked` stripped as hop-by-hop), send `Connection: close` and set `self.close_connection = True`.
- [x] 2.3 Raise the proxy listener backlog in `start_proxy` (set `request_queue_size` well above the stdlib default of 5, **before** the socket is activated, e.g. via a `ThreadingHTTPServer` subclass class attribute).
- [x] 2.4 Confirm all existing #1390 hardening is preserved (chunked request-body forwarding, RFC 7230 §3.3.3, bounded `_fail` drain, `log(..., RED)` error surfacing) and the new tests from Section 1 now pass.

## 3. CI wiring & verification

- [x] 3.1 Ensure the new test runs in the `e2e-harness-unit` check (it already copies `run.py` and sets `NCPS_RUN_PY` per #1390; confirm the new test file is picked up and add wiring if needed in `nix/e2e-tests/flake-module.nix`).
- [x] 3.2 Run the existing `test_dev_proxy.py` to confirm no regression in chunked-body forwarding / `_fail` behavior.
- [x] 3.3 Run `task fmt`, `task lint`, and `task test` and confirm each exits 0.
- [x] 3.4 Sync the `dev-ha-proxy` delta spec into `openspec/specs/` and archive the change.
