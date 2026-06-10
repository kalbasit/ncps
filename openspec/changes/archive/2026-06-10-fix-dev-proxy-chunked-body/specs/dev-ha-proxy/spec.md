## MODIFIED Requirements

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

## ADDED Requirements

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
