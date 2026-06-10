## ADDED Requirements

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

The proxy SHALL stream request and response bodies between client and backend rather than buffering whole payloads in memory, so that large NAR and narinfo transfers do not scale proxy memory with payload size.

#### Scenario: Large NAR transfer is streamed

- **WHEN** a client fetches a large NAR through the proxy
- **THEN** the proxy relays the body to the client using a bounded copy buffer
- **AND** the backend's response status, headers, and body are preserved end to end

#### Scenario: Upload request body is forwarded

- **WHEN** a client sends a request with a body (e.g. a `PUT` upload) through the proxy
- **THEN** the proxy forwards the request method, path, headers, and streamed body to the selected backend
- **AND** returns the backend's response to the client

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
