## ADDED Requirements

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
