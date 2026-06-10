# dev-ha-proxy Specification

## Purpose

Front the multiple `serve` instances spawned by `dev-scripts/run.py` in HA mode with a single round-robin reverse proxy, so a Nix client (or test driver) can target one stable endpoint instead of juggling per-instance ports. The proxy is a dev-harness convenience implemented with the Python standard library only.

## Requirements

### Requirement: Round-robin proxy fronts active instances in HA mode

When `dev-scripts/run.py` starts more than one ncps instance (`--replicas > 1`), it SHALL start an HTTP reverse proxy bound to port `8500` that forwards each incoming request to one of the active ncps instances, selecting backends in round-robin order across requests.

#### Scenario: Proxy starts in HA mode

- **WHEN** `run.py` is invoked with `--replicas 2` (or more) and the instances start successfully
- **THEN** an HTTP proxy is listening on `127.0.0.1:8500`
- **AND** the set of backends equals the started instance ports (e.g. `8501`, `8502`)

#### Scenario: Requests rotate across backends

- **WHEN** multiple HTTP requests arrive at the proxy on port `8500`
- **THEN** the proxy forwards consecutive requests to different instances in round-robin order
- **AND** each request reaches exactly one backend instance

### Requirement: Proxy is not started for single-instance runs

When `run.py` starts exactly one ncps instance (`--replicas 1`, the default), it SHALL NOT start the port-`8500` proxy.

#### Scenario: Single instance skips the proxy

- **WHEN** `run.py` is invoked with the default `--replicas 1`
- **THEN** no proxy is bound to port `8500`
- **AND** the single instance is reachable directly on its own port

### Requirement: Proxy streams request and response bodies

The proxy SHALL stream request and response bodies between client and backend rather than buffering whole payloads in memory, so that large NAR and narinfo transfers do not scale proxy memory with payload size. The proxy SHALL forward the request body regardless of how the client frames it — whether the body length is declared via `Content-Length` or the body is sent with `Transfer-Encoding: chunked`. When the client supplies a body, the proxy SHALL NOT forward a bodiless request to the backend.

#### Scenario: Large NAR transfer is streamed

- **WHEN** a client fetches a large NAR through the proxy
- **THEN** the proxy relays the body to the client using a bounded copy buffer
- **AND** the backend's response status, headers, and body are preserved end to end

#### Scenario: Content-Length upload request body is forwarded

- **WHEN** a client sends a `PUT` upload with a `Content-Length` header through the proxy
- **THEN** the proxy forwards the request method, path, headers, and streamed body to the selected backend
- **AND** the backend receives the complete request body byte-for-byte
- **AND** the proxy returns the backend's response to the client

#### Scenario: Chunked upload request body is forwarded

- **WHEN** a client sends a `PUT` upload with `Transfer-Encoding: chunked` and no `Content-Length` through the proxy
- **THEN** the proxy decodes the chunked request body from the client and delivers the complete body to the selected backend
- **AND** the backend receives the same number of body bytes the client streamed
- **AND** the backend does not receive an empty body
- **AND** the proxy returns the backend's response to the client

### Requirement: Proxy reports a clean error instead of resetting the client on backend failure

When the proxy fails to forward a request to a backend (the backend is unreachable, or the connection breaks mid-forward), it SHALL return an HTTP `502` response to the client rather than abruptly resetting the client connection. To do so, the proxy SHALL ensure any unconsumed client request body is drained or the response is delivered such that the client observes the `502` status, not a `Connection reset by peer`.

#### Scenario: Backend unreachable yields a 502

- **WHEN** the selected backend cannot be reached for a request that carries a body
- **THEN** the client receives an HTTP `502` response
- **AND** the client does not observe a connection reset (`curl error 56`)

#### Scenario: Backend failure mid-upload yields a 502

- **WHEN** the backend connection breaks while the proxy is forwarding a request body
- **THEN** the proxy returns an HTTP `502` to the client
- **AND** the proxy does not leave a partially-read client body that resets the client connection

### Requirement: Proxy surfaces forwarding errors for diagnosis

The proxy SHALL log backend connection and body-forwarding failures so that dev-harness upload failures are diagnosable, rather than silently discarding all proxy-side log output.

#### Scenario: Forwarding failure is logged

- **WHEN** the proxy encounters a backend connect failure or a body-forwarding error
- **THEN** the proxy emits a log line identifying the failed backend and the error
- **AND** the failure is not silently swallowed

### Requirement: Proxy supports HTTP/1.1 persistent client connections

The proxy SHALL serve HTTP/1.1 and keep client connections alive so that a keep-alive client (e.g. `nix copy`/libcurl) can reuse a single connection for multiple sequential requests, instead of closing every connection after one request. This prevents the connection-churn that resets uploads (`curl error 56: Connection reset by peer`) when pushing a large closure through the proxy.

#### Scenario: A reused client connection serves multiple requests

- **WHEN** a client sends two or more sequential requests on the same TCP connection through the proxy
- **THEN** the proxy answers `HTTP/1.1` and keeps the connection open after each delimitable response
- **AND** every request on the reused connection receives its response without the connection being reset

#### Scenario: A large parallel upload completes without a reset

- **WHEN** a client uploads many objects (narinfo and NAR files) through the proxy using keep-alive connections, as `nix copy` does for a large closure
- **THEN** the proxy reuses connections rather than forcing a new TCP connection per object
- **AND** the client does not observe a connection reset (`curl error 56`) that aborts the upload

#### Scenario: Expect: 100-continue is answered

- **WHEN** a client sends a request with an `Expect: 100-continue` header through the proxy
- **THEN** the proxy responds with an interim `100 Continue` before the client streams the request body
- **AND** the upload proceeds without a per-request stall

### Requirement: Proxy frames forwarded responses for safe connection reuse

The proxy SHALL frame every forwarded backend response so that a persistent connection stays synchronized for the next request. It MUST preserve a backend `Content-Length`, treat `204`/`304` responses, responses to `HEAD` requests, and `1xx` responses as bodiless, and — when the response body length is otherwise unknown (for example the backend used `Transfer-Encoding: chunked`, which the proxy strips as a hop-by-hop header) — signal `Connection: close` and stop reusing that connection so the client reads the body to end-of-stream rather than mis-framing the following response.

#### Scenario: Delimitable response keeps the connection alive

- **WHEN** the backend response is bodiless (`204`/`304`/`HEAD`) or carries a `Content-Length`
- **THEN** the proxy forwards it with intact framing and keeps the client connection open for reuse

#### Scenario: Unframed response falls back to connection close

- **WHEN** the backend response has no `Content-Length` and is not bodiless (its body length is unknown to the proxy)
- **THEN** the proxy adds `Connection: close` to the forwarded response and closes the connection after the body
- **AND** the client reads the body to end-of-stream and does not reuse the connection

### Requirement: Proxy lifecycle is tied to run.py

The proxy SHALL start and stop together with `run.py`. On `run.py` shutdown (SIGINT/SIGTERM or cleanup), the proxy SHALL be stopped and its listening port released.

#### Scenario: Proxy shuts down with run.py

- **WHEN** `run.py` receives a termination signal and runs its cleanup path
- **THEN** the proxy stops accepting connections
- **AND** port `8500` is released

### Requirement: Proxy endpoint is discoverable in state.json

When the proxy is started, `run.py` SHALL record its endpoint in `var/ncps/state.json` so test drivers and clients can discover the single HA entry point.

#### Scenario: State file advertises the proxy

- **WHEN** the proxy is started in HA mode
- **THEN** `var/ncps/state.json` contains the proxy endpoint (host and port `8500`)

#### Scenario: State file omits proxy when absent

- **WHEN** `run.py` runs a single instance and starts no proxy
- **THEN** `var/ncps/state.json` does not advertise a proxy endpoint
