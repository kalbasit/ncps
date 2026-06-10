## 1. Regression test (TDD — write failing first)

- [x] 1.1 Add a dev-harness test that imports the real `make_proxy_handler`/`start_proxy` from `dev-scripts/run.py`, starts the proxy in front of an in-test capture backend that records received request-body byte length, following existing `run.py`/e2e-harness test conventions.
- [x] 1.2 Assert a `Content-Length` PUT delivers the full N body bytes to the backend (passes today — guards against regression).
- [x] 1.3 Assert a `Transfer-Encoding: chunked` PUT delivers the full N body bytes to the backend (fails today — backend currently receives 0 bytes).
- [x] 1.4 Assert a request to an unreachable backend returns HTTP `502` to the client and does not reset the connection.
- [x] 1.5 Run the test and confirm 1.3 (and 1.4 if not yet handled) fail for the expected reasons before implementing.

## 2. Forward chunked request bodies

- [x] 2.1 In `_proxy()`, detect chunked framing from the client `Transfer-Encoding` header before it is dropped as hop-by-hop.
- [x] 2.2 Extract body forwarding into a single `_forward_request_body(conn)` helper that handles both `Content-Length` (existing loop) and `Transfer-Encoding: chunked`.
- [x] 2.3 Implement chunked decode from `self.rfile` (parse `<hex-size>[;ext]\r\n`, read that many bytes + trailing `\r\n`, stop at `0\r\n` + trailers + blank line), streaming each piece with the existing `PROXY_BUFFER` bound — no whole-body buffering.
- [x] 2.4 Re-encode to the backend as `Transfer-Encoding: chunked` (send the header, re-frame each piece, end with the zero-length terminator) so memory stays bounded.
- [x] 2.5 Treat malformed chunk framing defensively (logged `400`/`502`, no hang), consistent with the existing defensive `Content-Length` parse.
- [x] 2.6 Run the test suite; confirm tasks 1.2 and 1.3 now pass.

## 3. Harden the error path against client resets

- [x] 3.1 On backend `OSError` (connect or mid-forward), set `self.close_connection = True` and best-effort bounded-drain any unconsumed client body around `send_error(502, ...)` so the client observes the `502` instead of a `Connection reset by peer`.
- [x] 3.2 Run the test; confirm task 1.4 passes (502, no reset).

## 4. Surface proxy-side errors

- [x] 4.1 Keep per-request access logging quiet, but emit an explicit log line (backend address + exception) on backend connect failures and body-forwarding failures, so dev upload failures are diagnosable.

## 5. Verify and finalize

- [x] 5.1 Run `task fmt` and confirm it exits clean.
- [x] 5.2 Run `task lint` and confirm it exits clean.
- [x] 5.3 Run `task test` (plus the new proxy regression test) and confirm all pass.
- [x] 5.4 Confirm no changes were made outside `dev-scripts/run.py` and its test (ncps server code untouched).
