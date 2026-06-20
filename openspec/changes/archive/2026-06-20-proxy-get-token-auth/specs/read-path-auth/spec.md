## ADDED Requirements

### Requirement: Optional Bearer token for read paths

The server SHALL support an optional read-path authentication token, configured
via the `--cache-get-token` flag (env `CACHE_GET_TOKEN`). When the token is
non-empty, the server SHALL require an `Authorization: Bearer <token>` header
that matches the configured token on every `GET` and `HEAD` request, and SHALL
reject non-matching requests with `401 Unauthorized`. When the token is empty
(the default), the server SHALL serve read paths without authentication,
preserving existing behavior.

#### Scenario: No token configured allows anonymous reads

- **WHEN** no token is configured and a `GET` request arrives without an `Authorization` header
- **THEN** the server processes the request normally (no `401` is returned by the auth gate)

#### Scenario: Correct token allows GET

- **WHEN** a token is configured and a `GET` request carries `Authorization: Bearer <token>` matching the configured token
- **THEN** the server processes the request normally

#### Scenario: Correct token allows HEAD

- **WHEN** a token is configured and a `HEAD` request carries `Authorization: Bearer <token>` matching the configured token
- **THEN** the server processes the request normally

#### Scenario: Missing Authorization header is rejected

- **WHEN** a token is configured and a `GET` or `HEAD` request arrives without an `Authorization` header
- **THEN** the server responds with `401 Unauthorized`

#### Scenario: Wrong token is rejected

- **WHEN** a token is configured and a request carries `Authorization: Bearer <wrong>` that does not match the configured token
- **THEN** the server responds with `401 Unauthorized`

#### Scenario: Malformed Authorization scheme is rejected

- **WHEN** a token is configured and a request carries an `Authorization` header that does not use the `Bearer ` scheme (e.g. `Basic ...`)
- **THEN** the server responds with `401 Unauthorized`

### Requirement: Token comparison resists timing attacks

The server SHALL compare the presented Bearer token against the configured token
using a constant-time comparison so that the per-request processing time does
not reveal how many leading bytes of the secret are correct.

#### Scenario: Comparison is constant-time

- **WHEN** the presented token is compared against the configured token
- **THEN** the comparison uses a constant-time primitive (`crypto/subtle.ConstantTimeCompare`) rather than ordinary string equality

### Requirement: 401 responses advertise the Bearer scheme

The server SHALL include a `WWW-Authenticate: Bearer` header on `401 Unauthorized`
responses produced by the read-path auth gate, per RFC 7235.

#### Scenario: Unauthorized response carries WWW-Authenticate

- **WHEN** the read-path auth gate rejects a request with `401 Unauthorized`
- **THEN** the response includes a `WWW-Authenticate: Bearer` header

### Requirement: Infrastructure routes are always exempt

The server SHALL always serve the `/healthz` and `/metrics` infrastructure
routes without requiring the read-path token, regardless of whether a token is
configured.

#### Scenario: healthz is exempt

- **WHEN** a token is configured and a `GET /healthz` request arrives without an `Authorization` header
- **THEN** the server responds with `200 OK`

#### Scenario: metrics is exempt

- **WHEN** a token is configured and a `GET /metrics` request arrives without an `Authorization` header
- **THEN** the read-path auth gate does not return `401` for that route

### Requirement: Write verbs are unaffected by the read-path token

The read-path token SHALL apply only to `GET` and `HEAD` requests. `PUT` and
`DELETE` requests SHALL continue to be governed solely by their existing
`putPermitted` / `deletePermitted` guards and SHALL NOT be additionally gated by
the read-path token.

#### Scenario: PUT is not gated by the read-path token

- **WHEN** a token is configured and a `PUT` request arrives without a matching `Authorization` header
- **THEN** the read-path auth gate does not return `401`, and the request is handled by the existing PUT guard

### Requirement: Kubernetes deployments source the token from a Secret

The Helm chart SHALL deliver the read-path token to the container as the
`CACHE_GET_TOKEN` environment variable sourced from a Kubernetes Secret (a
chart-managed Secret when an inline value is provided, or an operator-supplied
existing Secret), and SHALL NOT place the token in the plaintext ConfigMap. When
no token is configured, the chart SHALL NOT inject the env var or create a Secret
key for it.

#### Scenario: Inline token value creates a managed Secret key and env var

- **WHEN** an inline read-path token value is set in chart values
- **THEN** the rendered manifests include a Secret holding the token and a `CACHE_GET_TOKEN` env var with a `secretKeyRef` to it, and the ConfigMap contains no plaintext token

#### Scenario: Existing Secret is referenced

- **WHEN** the chart values reference an existing Secret for the read-path token
- **THEN** the rendered `CACHE_GET_TOKEN` env var uses a `secretKeyRef` pointing at that existing Secret

#### Scenario: No token leaves deployments unchanged

- **WHEN** no read-path token is configured in chart values
- **THEN** the rendered manifests contain no `CACHE_GET_TOKEN` env var and no token Secret key
