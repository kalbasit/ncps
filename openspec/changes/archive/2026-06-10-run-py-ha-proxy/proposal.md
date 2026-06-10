## Why

`dev-scripts/run.py` can already boot N ncps replicas for HA mode (ports `8501`, `8502`, …), but a Nix client must be pointed at a single substituter URL. There is no stable front door, so developers cannot exercise HA behavior (cross-instance cache fills, shared locker/storage coordination) from a real `nix` client without manually juggling per-instance ports.

## What Changes

- Add a lightweight round-robin reverse proxy, implemented with the Python standard library inside `run.py`, listening on a fixed port `8500`.
- The proxy forwards every incoming HTTP request to one of the active ncps instances, rotating backends round-robin so successive requests (e.g. narinfo lookup then NAR fetch) land on different replicas — naturally exercising HA.
- The proxy is started automatically whenever HA mode is active (`--replicas > 1`); single-instance runs do not start it.
- Backends are derived from the instances `run.py` just launched (the same list written to `var/ncps/state.json`).
- The proxy streams request and response bodies (NAR/narinfo payloads can be large) rather than buffering them in memory.
- The proxy lifecycle is tied to `run.py`: it shuts down cleanly via the existing signal/cleanup path, and its endpoint is recorded in `var/ncps/state.json` so test drivers can discover it.

## Non-goals

- No production-grade load balancing (health checks, retries, sticky sessions, weighted routing, circuit breaking). This is a dev-only convenience.
- No new Nix flake dependency (no nginx/caddy/HAProxy).
- No TLS termination — the proxy serves plain HTTP, matching the instances.
- Not started for single-instance (`--replicas 1`) runs.
- No change to ncps application code or its HA semantics; this only affects the dev harness.

## Capabilities

### New Capabilities

- `dev-ha-proxy`: A round-robin HTTP reverse proxy in `dev-scripts/run.py` that fronts all active ncps instances on port `8500` during HA dev runs, giving Nix clients a single stable endpoint.

### Modified Capabilities

<!-- None: this is a dev-harness-only addition; no existing spec requirements change. -->

## Impact

- **Code**: `dev-scripts/run.py` only (proxy server thread, startup gating on `--replicas > 1`, cleanup wiring, `state.json` endpoint field). No Go/application changes.
- **State file**: `var/ncps/state.json` gains a proxy endpoint entry for discovery by e2e/test drivers.
- **I/O / latency**: Adds one in-process hop per request on the dev machine; bodies are streamed, so steady-state memory is bounded by a fixed copy buffer, not payload size. Negligible added latency for local-loopback dev use.
- **Network**: Binds an additional local port (`8500`); no external exposure.
- **Dependencies**: None added — Python stdlib only.
