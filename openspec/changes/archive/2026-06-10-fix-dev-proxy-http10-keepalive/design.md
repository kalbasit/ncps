## Context

`dev-scripts/run.py` runs an HA round-robin reverse proxy on `:8500` (added in #1389) in front of N ncps `serve` instances. #1390 fixed it dropping `Transfer-Encoding: chunked` request bodies. The proxy is a stdlib-only `http.server.ThreadingHTTPServer` whose handler (`make_proxy_handler`) subclasses `BaseHTTPRequestHandler`.

`BaseHTTPRequestHandler.protocol_version` defaults to `"HTTP/1.0"`. With that default, after each response the handler sets `close_connection = True` and the connection is closed. A keep-alive client (libcurl, which `nix copy` uses) therefore cannot reuse connections through the proxy and opens a fresh TCP connection per request. For a large closure that is thousands of connections; the resulting churn — together with the stdlib default listen backlog of 5 and a ~1 s stall per PUT (the HTTP/1.0 handler never answers `Expect: 100-continue`) — starves the single-threaded accept loop and intermittently leaves nix's upload connection reset before the proxy processes it (`curl error 56`), aborting the copy.

Reproduced deterministically: isolated clean ncps (sqlite, `--cache-lock-backend=local`) behind the real proxy → exact `curl error 56`. Patched proxy (`protocol_version="HTTP/1.1"` + response framing + larger backlog) → 3488 PUTs, 0 resets; `curl -v` shows `HTTP/1.1 200` + "Connection left intact".

## Goals / Non-Goals

**Goals:**
- The dev proxy supports HTTP/1.1 persistent connections so a keep-alive client reuses connections, eliminating the connection-churn that triggers `curl error 56` on large `nix copy` uploads.
- Forwarded responses remain correctly framed so a reused connection never desyncs.
- Remove the per-PUT `Expect: 100-continue` stall (a free consequence of HTTP/1.1).
- A regression test that drives the real proxy and fails on the current HTTP/1.0 behavior.

**Non-Goals:**
- No ncps server changes — ncps is not at fault.
- No change to the round-robin backend selection, request-body forwarding (#1390), or the `_fail`/502 error path.
- Not implementing response re-chunking; unknown-length responses fall back to `Connection: close` (correct, just not reused).
- No production (non-dev) impact.

## Decisions

1. **`ProxyHandler.protocol_version = "HTTP/1.1"`.** This is the core fix. It makes connections persistent (client reuse) and makes `BaseHTTPRequestHandler` auto-answer `Expect: 100-continue` (it gates that on `protocol_version >= "HTTP/1.1"`), removing the ~1 s-per-PUT stall. Default keep-alive behavior then depends on correct response framing (next decision).

2. **Frame every forwarded response.** When relaying the backend response: keep `Content-Length` if present; treat `204`/`304`, `HEAD` requests, and `1xx` as bodiless (no body, connection reusable). If the body length is otherwise unknown — e.g. the backend used `Transfer-Encoding: chunked`, which the proxy already strips as a hop-by-hop header — send `Connection: close` and set `self.close_connection = True` so the client reads the body to EOF and does not attempt to reuse the connection. Upload responses (`204`, `404` with `Content-Length`, narinfo `GET` with `Content-Length`) are all delimitable, so keep-alive engages for the hot path; only genuinely unframed responses fall back to close. This keeps correctness absolute while capturing the reuse benefit.

3. **Raise the listener backlog.** Set `request_queue_size` (e.g. 128) on the server **before** `server_activate()` listens — i.e. via a `ThreadingHTTPServer` subclass class attribute or by setting it prior to bind/activate — so bursts of connection establishment are absorbed. With keep-alive in place this is defense-in-depth, but it is cheap and removes the backlog-5 cliff.

4. **Preserve all #1390 hardening.** Chunked request-body forwarding, RFC 7230 §3.3.3 (chunked wins over Content-Length; don't forward CL to backend when chunked), the bounded `_fail` drain, and `log(..., RED)` error surfacing are unchanged.

## Risks / Trade-offs

- **Response mis-framing on a persistent connection** would corrupt the next response. Mitigated by decision 2: anything not provably delimitable forces `Connection: close`. The conservative default is "close", never "reuse with a guessed length".
- **`HEAD` semantics**: a `HEAD` response may carry a `Content-Length` describing the entity but no body; the handler must not try to read a body for `HEAD`. Treated as bodiless regardless of headers.
- **Idle persistent connections** hold a worker thread in `ThreadingHTTPServer` until the client closes or times out. For a dev harness under a finite `nix copy` this is bounded and far cheaper than the thousands-of-threads churn it replaces; `BaseHTTPRequestHandler.timeout` still applies.
- **Backlog set too late** (on the instance after activation) would silently no-op; the implementation must set it before `server_activate()`.
