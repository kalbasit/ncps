## 1. Test scaffolding (TDD: red first)

- [x] 1.1 Add a test module for the proxy (e.g. `dev-scripts/test_run_proxy.py`) with stdlib `unittest`; set up a helper that starts 1–2 stub HTTP backends on ephemeral ports returning identifiable bodies/headers.
- [x] 1.2 Write a failing test for `pick_backend(backends, counter)` asserting round-robin rotation and wrap-around.
- [x] 1.3 Write a failing test that the proxy forwards a `GET` to a backend and preserves status code, response headers, and body.
- [x] 1.4 Write a failing test that consecutive requests rotate across two backends.
- [x] 1.5 Write a failing test that a `PUT` request body is streamed/forwarded to the selected backend and the backend response is returned.
- [x] 1.6 Write a failing test that a dead/refused backend yields a `502` (proxy thread does not crash).
- [x] 1.7 Write a failing test for lifecycle: starting then shutting down the proxy releases port `8500` (or an ephemeral port in tests).

## 2. Core proxy implementation (green)

- [x] 2.1 Implement `pick_backend(backends, counter)` round-robin selection guarded by a `threading.Lock`.
- [x] 2.2 Implement a `BaseHTTPRequestHandler` subclass (or factory bound to a backend list) that proxies `GET`/`HEAD`/`PUT`/`POST` to the chosen backend.
- [x] 2.3 Strip hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-*`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`), reset `Host` to the backend, and forward remaining request headers.
- [x] 2.4 Stream request and response bodies with a bounded buffer via `shutil.copyfileobj`; preserve backend status and response headers.
- [x] 2.5 Return `502` with the backend error when a backend connection fails; keep the server thread alive.
- [x] 2.6 Provide a `start_proxy(host, port, backends)` that runs `ThreadingHTTPServer` in a daemon thread and returns the server handle.

## 3. Integrate into run.py lifecycle

- [x] 3.1 After the instance-launch loop, start the proxy on `127.0.0.1:8500` only when `args.replicas > 1`, using the launched instance ports as backends.
- [x] 3.2 Fail fast with a clear error if binding port `8500` fails (e.g. already in use).
- [x] 3.3 Store the server handle and stop it in `cleanup()` via `server.shutdown()` + `server_close()`; verify port release.
- [x] 3.4 Add a startup banner line showing the proxy endpoint when active.

## 4. State file + discovery

- [x] 4.1 Extend `write_state_file`/`state_config` to record `{"proxy": {"host": "127.0.0.1", "port": 8500}}` only when the proxy is started.
- [x] 4.2 Ensure the `proxy` field is omitted for single-instance runs.
- [x] 4.3 Add/extend a test asserting `state.json` advertises the proxy in HA mode and omits it otherwise.

## 5. Verify & document

- [x] 5.1 Run the proxy test module and confirm all tests pass (red → green).
- [x] 5.2 Manually verify: `run.py --replicas 2 --locker redis --db postgres` exposes `127.0.0.1:8500`, requests rotate across `8501`/`8502`, and a Nix client can use `http://127.0.0.1:8500` as substituter.
- [x] 5.3 Confirm `cleanup()` releases port `8500` on shutdown.
- [x] 5.4 Run `task fmt` and `task lint` (and any Python lint the repo applies) and confirm clean.
