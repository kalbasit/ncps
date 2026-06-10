## Why

After PR #1390 fixed `Transfer-Encoding: chunked` request-body forwarding, `nix copy` of a large closure (e.g. a ~9.35 GB `nixos-system`) to the dev HA proxy on `:8500` **still** fails intermittently with `error: ... Recv failure: Connection reset by peer (curl error code=56)`, aborting the entire copy on the first reset. Small/single uploads succeed; the failure only appears under the real parallel large-closure workload.

Root cause (empirically reproduced on a clean isolated ncps behind the real proxy, then fix-validated): the proxy's `ProxyHandler` leaves `protocol_version` at the stdlib `BaseHTTPRequestHandler` default of **HTTP/1.0**, so it **force-closes every client connection after one request**. `nix copy` drives hundreds of objects over ~25 parallel HTTP/1.1 keep-alive connections, but because the proxy refuses to keep any connection alive, nix must open a brand-new TCP connection for every narinfo/NAR/HEAD — thousands of short-lived connections. Under that churn (amplified by the stdlib default listen backlog of 5 and a ~1 s-per-PUT stall because the proxy never answers `Expect: 100-continue`), the single-threaded accept loop stalls (a ~19 s stall was measured) and intermittently nix's upload connection is reset **before the proxy ever processes it** — surfacing as `curl error 56`. The failed NAR shows only a `HEAD→404` with no PUT on any backend and no proxy-side error log; a lone secondary PUT-500 (`error writing the nar to the temporary file: read ... connection reset by peer`) is nix tearing down in-flight uploads after the abort, not the primary cause.

This is a **second, distinct** dev-harness bug from the #1390 chunked-body fix (which is necessary and stays). The fix is confined to `dev-scripts/run.py`; ncps server code is not at fault.

## What Changes

- The dev HA proxy (`dev-scripts/run.py` `make_proxy_handler`) speaks **HTTP/1.1 with persistent connections**, so a keep-alive client (nix/libcurl) reuses a small pool of connections across the whole copy instead of being forced into thousands of short-lived ones. As a direct consequence, `BaseHTTPRequestHandler` now auto-answers `Expect: 100-continue` (it only does so for `protocol_version >= HTTP/1.1`), removing the ~1 s-per-PUT stall.
- The proxy frames every forwarded response so a persistent connection stays in sync: preserve `Content-Length`; treat `204`/`304`/`HEAD`/`1xx` as bodiless; and when the response body length is unknown (e.g. the backend used `Transfer-Encoding: chunked`, which is stripped as hop-by-hop), send `Connection: close` and stop reusing that connection so the client reads to EOF instead of mis-framing the next response.
- The proxy listener backlog is raised well above the stdlib default of 5 (set before the socket is activated) to absorb connection bursts.
- All existing #1390 hardening is preserved: chunked request-body forwarding, RFC 7230 §3.3.3 handling, the bounded `_fail` drain, and proxy error logging.
- A regression test drives the real `run.py` proxy (imported via `NCPS_RUN_PY`, like the existing `test_dev_proxy.py`) asserting the proxy answers HTTP/1.1, a single client connection is reused for multiple sequential requests without a reset, and responses stay correctly framed. It is wired into the `e2e-harness-unit` check.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `dev-ha-proxy`: the proxy MUST support HTTP/1.1 persistent client connections (connection reuse) and frame forwarded responses so persistent connections stay synchronized, rather than closing every connection after a single request.

## Impact

- **Code**: `dev-scripts/run.py` (proxy handler `protocol_version`, response-framing in `_proxy`, listener backlog in `start_proxy`). No ncps server changes.
- **Tests**: new regression test in `nix/e2e-tests/tests/` (sibling to `test_dev_proxy.py`); wired into `checks.e2e-harness-unit` in `nix/e2e-tests/flake-module.nix`.
- **Spec**: `openspec/specs/dev-ha-proxy/spec.md` (modified requirement on streaming/connection handling).
- **Runtime behavior**: dev-only. Eliminates `curl error 56` aborts when pushing large closures through `:8500`; reduces connection churn and removes per-PUT `Expect` stalls.
