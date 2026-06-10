## Context

`dev-scripts/run.py` runs an in-process, stdlib-only round-robin reverse proxy (`make_proxy_handler` / `start_proxy`, a `ThreadingHTTPServer` of `BaseHTTPRequestHandler`) on port `8500` to front the HA `serve` instances (PR #1389). Its `_proxy()` handler forwards the client request to a backend via `http.client.HTTPConnection`.

The request-body forwarding is gated on `Content-Length`:

```python
content_length = None
length_header = self.headers.get("Content-Length")
if length_header is not None:
    content_length = int(length_header)
...
if content_length is not None:        # run.py ~319
    remaining = content_length
    while remaining > 0:
        chunk = self.rfile.read(min(PROXY_BUFFER, remaining))
        ...
        conn.send(chunk)
```

`Transfer-Encoding` is in `HOP_BY_HOP_HEADERS` and is stripped from the forwarded headers. So a `Transfer-Encoding: chunked` request reaches `_proxy()` with no `Content-Length`; `content_length` stays `None`; the body loop is skipped entirely; and the backend receives a bodiless `PUT`. The client's body is left unread in `self.rfile`, and on the keep-alive connection that surfaces to the client as `curl error 56: Connection reset by peer`.

Empirically verified against the real `run.py` proxy: a `Content-Length` PUT delivered 200000 body bytes to a capture backend; an identical `Transfer-Encoding: chunked` PUT delivered **0** body bytes. `nix copy` uses chunked encoding for NAR uploads because the on-the-fly-compressed `.nar.zst` size is unknown ahead of time, so large closures (e.g. `nixos-system`) fail while small `Content-Length` NARs succeed.

This is a dev-harness fault only. ncps server code is correct and out of scope.

## Goals / Non-Goals

**Goals:**
- The proxy forwards request bodies for both `Content-Length` and `Transfer-Encoding: chunked` framings, delivering the complete body to the backend.
- Backend failures yield a clean `502` to the client, never a TCP reset from an unconsumed request body.
- Backend connect/forward failures are logged proxy-side so dev upload failures are diagnosable.
- A regression test drives the actual `run.py` proxy with both framings and asserts full-body delivery.

**Non-Goals:**
- No change to ncps server (`pkg/...`) code or behavior.
- No production runtime change; the proxy is a dev convenience only.
- No new external dependency — implementation stays within the Python standard library.
- Not introducing whole-payload buffering; body forwarding remains streamed with the existing bounded buffer.

## Decisions

### Decision 1: Detect chunked framing and stream-decode the client body

When `Content-Length` is absent, inspect the client's `Transfer-Encoding` header (before it is dropped as hop-by-hop). If it contains `chunked`, read the body by decoding the chunk framing from `self.rfile` (read each `<hex-size>\r\n`, then that many bytes, then the trailing `\r\n`, until the `0\r\n\r\n` terminator), streaming each decoded chunk to the backend with the existing `PROXY_BUFFER` bound.

Forward to the backend re-encoded as chunked: send `Transfer-Encoding: chunked` to the backend and re-frame each decoded piece, ending with the zero-length terminator. This preserves streaming (no need to know the total size, no whole-body buffering).

**Why re-encode chunked rather than buffer-and-set-Content-Length:** buffering the whole body to compute a `Content-Length` would make proxy memory scale with payload size — exactly the property the streaming design exists to avoid, and these are multi-hundred-MB NAR closures. Re-encoding chunked keeps memory bounded. Go's `net/http` backend accepts a chunked request body transparently.

**Alternative considered — pass the client body through verbatim by not stripping `Transfer-Encoding`:** rejected. `http.client.HTTPConnection` does not transparently relay an already-chunked stream; the proxy must own the framing. Explicit decode/re-encode is unambiguous and directly testable.

### Decision 2: Unify the body-forwarding into a single helper

Refactor the `Content-Length` loop and the new chunked loop behind one `_forward_request_body(conn)` that picks the strategy from the client headers. Keeps `_proxy()` readable and gives the regression test a single seam if needed.

### Decision 3: Error path drains/aborts before responding 502

On an `OSError` raised while connecting or forwarding, before/around `send_error(502, ...)`, ensure the client connection does not reset due to an unconsumed body. Set `self.close_connection = True` and best-effort drain remaining readable client body (bounded) so the `502` is what the client sees. Where a clean `502` is impossible (body already partially consumed and backend gone), prefer a deterministic close over a silent reset, and log it.

**Why:** `BaseHTTPRequestHandler` resets the socket when the handler returns with unread request data on a keep-alive connection. Draining (or forcing close after writing the response) converts the reset into an observable `502`.

### Decision 4: Make proxy errors visible

`log_message` is currently overridden to a no-op. Keep request access logging quiet (instances log their own), but add explicit `log(...)`/stderr lines for backend connect failures and body-forward failures, including the backend address and the exception. This is the only signal the proxy emits today, so it must not be swallowed.

### Decision 5: TDD regression test driving the real proxy

Add a test that imports `make_proxy_handler`/`start_proxy` from `run.py`, points the proxy at an in-test capture backend that records received body length, and asserts:
- `Content-Length` PUT → backend receives the full N bytes.
- `Transfer-Encoding: chunked` PUT → backend receives the full N bytes (currently 0 — the failing test).
- Backend-unreachable PUT → client gets `502`, not a reset.

This mirrors the manual reproduction already performed. Test placement and runner follow the existing dev-harness test conventions (the same approach used for other `run.py` / e2e-harness unit tests).

## Risks / Trade-offs

- **[Chunked decode edge cases — chunk extensions, trailers]** → Decode defensively: parse the size token up to the first `;` (ignore chunk extensions), and consume trailer lines until the blank line after the `0` chunk. nix/curl do not emit trailers, but the parser must not hang on them.
- **[Malformed chunk framing from a client could hang or loop]** → Bound reads with timeouts/expected terminators and treat a framing error as a `400`/`502` with a logged reason, consistent with the existing defensive `Content-Length` parse that returns `400`.
- **[Re-encoding chunked changes the exact bytes on the wire (framing), though not the payload]** → Acceptable: HTTP semantics are preserved and the backend reassembles the identical payload; the test asserts payload-byte equality, not wire-byte equality.
- **[Draining a large unconsumed body on the error path could be slow]** → Bound the drain; if the body is huge and the backend is already gone, force-close rather than drain unboundedly. The common error case (backend unreachable) happens before much body is read.
- **[Scope creep into ncps server code]** → Explicitly out of scope; the change touches `dev-scripts/run.py` and its test only.

## Migration Plan

No deployment or data migration. Dev-only change: edit `dev-scripts/run.py`, add the regression test, verify `task fmt` / `task lint` / `task test` plus the new test. Rollback is reverting the commit. No state or schema impact.

## Open Questions

- Exact home for the regression test (alongside existing `run.py`/e2e-harness unit tests) and whether it should be wired into `nix flake check` like the existing `e2e-harness-unit` net — confirm during apply against current harness-test conventions.
