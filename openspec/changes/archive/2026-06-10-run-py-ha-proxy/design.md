## Context

`dev-scripts/run.py` boots N ncps replicas for HA testing on ports `8501..850N`, gating HA on a distributed locker and non-sqlite DB. It already tracks instances in `processes`/`tmux_pids`, writes `var/ncps/state.json`, and tears everything down via `cleanup()`. What's missing is a single client-facing endpoint: a Nix substituter can only point at one URL, so exercising HA from a real client today means manually picking one replica port (defeating the purpose).

This change adds a round-robin reverse proxy on port `8500`, in-process in `run.py`, started only when `--replicas > 1`. It is dev-harness-only: no Go changes, no new flake dependency.

## Goals / Non-Goals

**Goals:**
- Single stable endpoint (`127.0.0.1:8500`) fronting all active instances in HA mode.
- Round-robin backend selection so consecutive requests (narinfo → NAR) hit different replicas, exercising cross-instance HA.
- Stream bodies (large NARs, uploads) without buffering whole payloads.
- Lifecycle bound to `run.py`: starts after instances launch, stops on cleanup.
- Discoverable: proxy endpoint recorded in `state.json`.

**Non-Goals:**
- Production LB features: health checks, retries, sticky sessions, weighting, circuit breaking.
- TLS termination; new flake dependencies (nginx/caddy); any ncps application change.
- Running the proxy for single-instance (`--replicas 1`) runs.

## Decisions

### D1: Python stdlib `ThreadingHTTPServer` + a `BaseHTTPRequestHandler` subclass

Use `http.server.ThreadingHTTPServer` with a custom handler that proxies each request to a backend chosen round-robin. Forward via `urllib.request` (stdlib) with `urlopen` and stream the response to the client.

- *Why*: zero new dependencies (rule from proposal), matches the existing all-stdlib `run.py`. Threading server gives concurrent request handling so a long NAR stream doesn't block narinfo lookups.
- *Alternative — `http.client` raw*: more control over hop-by-hop headers but more code; chosen only where `urllib` is insufficient (see D4).
- *Alternative — external caddy/nginx*: rejected (flake dependency, config generation, heavier for dev).

### D2: Round-robin via a shared, lock-guarded counter

A module-level index incremented under a `threading.Lock`, `backend = backends[idx % len(backends)]`. Backends are the instance ports captured in `instance_info` after launch.

- *Why*: trivial, fair, and stateless across requests; thread-safe under the threading server.
- *Alternative — random.choice*: also valid but the user selected round-robin for deterministic rotation in tests.

### D3: Run the proxy in a background thread owned by `run.py`'s main process

Start the `ThreadingHTTPServer` in a daemon thread after the instance loop, before `signal.pause()`. Hold a reference so `cleanup()` calls `server.shutdown()`/`server.server_close()`.

- *Why*: keeps a single process for lifecycle; integrates with the existing `cleanup()` and `signal.pause()` flow. Daemon thread guarantees no hang if shutdown races.
- *Note*: this lives in the parent `run.py` (the orchestrator), not the hidden `--internal-start-instance` wrapper.

### D4: Header handling — strip hop-by-hop, preserve the rest; bounded `shutil.copyfileobj`

Copy client request headers to the backend except hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-*`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Host` is reset to the backend). Stream request and response bodies with a fixed buffer (e.g. 64 KiB) via `shutil.copyfileobj`.

- *Why*: NAR/narinfo correctness depends on faithful status/headers; streaming bounds proxy memory regardless of payload size (spec requirement).
- *Edge*: forward `Content-Length`/chunked bodies for `PUT` uploads (the harness runs with `--cache-allow-put-verb`).

### D5: `state.json` gains a `proxy` field

`write_state_file` records `{"proxy": {"host": "127.0.0.1", "port": 8500}}` only when the proxy is started; omitted otherwise.

- *Why*: lets e2e/test drivers discover the single HA endpoint without guessing.

### D6: TDD approach

Drive the implementation test-first. Because `run.py` is a script, extract the proxy into importable, unit-testable units: a pure `pick_backend(backends, counter)` round-robin function and a handler factory bound to a backend list. Tests spin up the proxy against stub HTTP backends (stdlib `http.server` on ephemeral ports) and assert rotation, streaming, status/header passthrough, and lifecycle shutdown.

## Risks / Trade-offs

- **Port `8500` already in use** → fail fast with a clear error at bind time; do not silently continue without a front door.
- **A backend instance is slow/down mid-rotation** (no health checks by design) → request to that backend errors; acceptable for dev. Mitigation: return a `502` with the backend error rather than crashing the proxy thread; document the non-goal.
- **Hop-by-hop header leakage corrupting transfers** → explicit strip-list (D4) plus a passthrough test for status/headers and a streamed-body test.
- **Cleanup race leaving port `8500` bound** → daemon thread + explicit `server.shutdown()`/`server_close()` in `cleanup()`; assert port release in a lifecycle test.
- **Round-robin counter contention under load** → negligible for dev; guarded by a lock, O(1) per request.

## Migration Plan

Additive, dev-only. No deployment or rollback concerns: shipping the change adds proxy behavior to HA `run.py` invocations; reverting the commit removes it. No state/schema migration. Existing single-instance and non-HA workflows are unaffected.

## Open Questions

- None blocking. (Behavior on a dead backend is handled as a `502` per D-risk; revisit only if a richer dev story is requested.)
