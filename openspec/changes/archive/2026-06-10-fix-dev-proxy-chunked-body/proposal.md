## Why

The dev HA round-robin proxy in `dev-scripts/run.py` silently drops the body of any request sent with `Transfer-Encoding: chunked`, forwarding a bodiless request to the backend and leaving the client's body unconsumed — which the client sees as `curl error 56: Connection reset by peer`. `nix copy` uploads large NARs (e.g. a `nixos-system` closure) with chunked encoding because the on-the-fly-compressed `.nar.zst` size is unknown in advance, so large uploads through the HA proxy fail while small `Content-Length` ones succeed. This was reproduced against the actual `run.py` proxy: a `Content-Length` PUT delivered 200000 bytes to the backend; an identical chunked PUT delivered 0 bytes.

## What Changes

- Forward chunked request bodies: when `Content-Length` is absent but the client used `Transfer-Encoding: chunked`, the proxy decodes the chunked framing from the client and delivers the full body to the backend (re-encoded chunked, or with an explicit `Content-Length`). The proxy never forwards a bodiless request when the client supplied a body.
- Harden the error path: on a backend `OSError` mid-forward, the proxy drains or hard-aborts the client request body around `send_error` so a partially-read body does not turn an intended `502` into a TCP reset. The client receives a real `502`, not a connection reset.
- Add proxy-side observability: surface backend connect/forward failures (currently `log_message` is silenced) so future proxy failures are diagnosable instead of invisible.
- Add a regression test driving the real `run.py` proxy with both a `Content-Length` and a `Transfer-Encoding: chunked` PUT, asserting the backend receives the full body in both cases.

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `dev-ha-proxy`: the "Proxy streams request and response bodies" requirement is extended so that upload bodies are forwarded regardless of framing (`Content-Length` or `Transfer-Encoding: chunked`); new requirements cover error-path reset avoidance and proxy-side error visibility.

## Impact

- **Code**: `dev-scripts/run.py` only — the `_proxy()` handler's request-body forwarding and error path, plus its `log_message`. ncps server code is **not** changed; it is not at fault.
- **Tests**: a new dev-harness regression test exercising the proxy's body forwarding for both framings.
- **I/O / network / memory**: bodies remain streamed with the existing bounded buffer; no whole-payload buffering is introduced, so proxy memory still does not scale with payload size. Chunked uploads gain correct end-to-end delivery; latency is unchanged.
- **Scope**: dev-harness only; no production or runtime-server behavior is affected.
