## ADDED Requirements

### Requirement: Transient upstream transport failures MUST be retried with bounded backoff

An idempotent (GET/HEAD) upstream request that fails transiently SHALL be retried with
bounded, capped backoff. A transient transport error means HTTP/2 `GOAWAY`, `http2:
timeout awaiting response headers`, connection reset, or broken pipe. The retry count
SHALL be bounded, the per-attempt **backoff delay capped**, and the wait SHALL respect
context cancellation. A genuine not-found (HTTP 404) response is not a transport error and
SHALL NOT be retried.

#### Scenario: Transient error is retried after a delay then succeeds

- **GIVEN** an upstream GET that fails once with a transient transport error then succeeds
- **WHEN** the request is performed
- **THEN** it SHALL be retried after a backoff delay and ultimately succeed

#### Scenario: Retries are bounded and backoff is capped

- **GIVEN** an upstream GET that fails repeatedly with a transient transport error
- **WHEN** the request is performed
- **THEN** the number of retries SHALL be bounded
- **AND** the per-attempt backoff SHALL not exceed a fixed cap
- **AND** the total added latency SHALL stay within a bounded budget

#### Scenario: Context cancellation aborts the backoff wait

- **GIVEN** a transient failure has triggered a backoff wait
- **WHEN** the request context is cancelled during the wait
- **THEN** the request SHALL return promptly with the context error rather than completing the delay

#### Scenario: Genuine 404 is not retried

- **GIVEN** an upstream request whose response is HTTP 404
- **WHEN** the request is performed
- **THEN** it SHALL NOT be retried
- **AND** the not-found result SHALL be surfaced to the caller
