# NAR Serving Latency Bounds

## Purpose

Guarantees that a NAR request resolves within a bounded time regardless of how slow the storage
layer is, so a stalled storage probe can never be delivered to a client as a truncated success,
and so slow storage is visible in logs and metrics instead of silent.

## Requirements

### Requirement: NAR requests MUST have a bounded time-to-first-byte

A NAR request SHALL either begin emitting response body bytes, or terminate with an explicit
error status, within a configured time-to-first-byte budget — regardless of how long the storage
layer takes to answer a presence probe. The budget SHALL default to a value comfortably below the
read timeout of a typical reverse proxy (60 s), and SHALL be configurable.

A NAR response that has already committed a `200` status and a `Content-Length` SHALL NOT be
allowed to stall such that an intermediary aborts it mid-body; the truncated-success outcome is
the failure this requirement exists to prevent.

#### Scenario: Storage presence probe stalls far beyond the budget

- **WHEN** a client requests a NAR
- **AND** the storage backend's presence probe blocks for 120 s
- **AND** the configured time-to-first-byte budget is 10 s
- **THEN** the request MUST resolve within approximately the budget, not 120 s
- **AND** the client MUST receive either the first body byte or a non-2xx status
- **AND** the client MUST NOT receive a `200` whose body is shorter than its declared `Content-Length`

#### Scenario: Several stalled probes in one request share the budget

- **WHEN** a single NAR request consults storage more than once (the pre-check, the
  servability lookup, and again after download coordination)
- **AND** every one of those probes stalls
- **THEN** the request MUST still resolve within approximately one budget, not one budget
  per probe
- **AND** the total MUST NOT scale with the number of probes the read path happens to make

#### Scenario: Fast storage is unaffected

- **WHEN** a client requests a NAR that is present in storage
- **AND** the presence probe returns promptly
- **THEN** the response MUST be served exactly as before, with no added latency
- **AND** no timeout-related log, metric, or error MUST be emitted

### Requirement: The storage presence probe MUST NOT block a request past its deadline

The storage presence probe issued while serving a NAR SHALL be bounded by a deadline. When the
deadline expires the request SHALL stop waiting on the probe and proceed to resolve, even when
the underlying probe cannot itself be cancelled.

A backend whose probe honours `context.Context` SHALL have the deadline propagated into the
probe so the underlying operation is genuinely cancelled. A backend whose probe cannot observe
context cancellation SHALL NOT hold the request goroutine hostage to it.

#### Scenario: Context-aware backend cancels the probe

- **WHEN** the storage backend's presence probe accepts a context
- **AND** the probe deadline expires
- **THEN** the probe operation MUST be cancelled
- **AND** no resources MUST remain reserved for it beyond the cancellation

#### Scenario: Uncancellable probe does not pin the request

- **WHEN** the storage backend's presence probe cannot observe context cancellation
- **AND** the probe deadline expires while the probe is still blocked
- **THEN** the request MUST proceed without waiting for the probe to return
- **AND** the abandoned probe MUST NOT prevent the request from resolving

### Requirement: A timed-out presence probe MUST NOT be reported as asset absence

A presence probe that times out SHALL be treated as an indeterminate result, never as a
determination that the asset is absent. Reporting a stalled probe as absence would silently
convert a slow read into a redundant re-download or a spurious 404.

#### Scenario: Timed-out probe is distinguished from a genuine miss

- **WHEN** the presence probe for a NAR times out
- **THEN** the outcome MUST be classified as indeterminate, distinct from "not present"
- **AND** the request MUST NOT return `404 Not Found` on the strength of the timeout alone

#### Scenario: Genuine absence is still reported as absence

- **WHEN** the presence probe completes promptly and reports the asset is not in storage
- **THEN** the existing cache-miss behaviour MUST apply unchanged

### Requirement: Slow and timed-out storage probes MUST be observable

A presence probe that exceeds a warning threshold, or that exceeds its deadline, SHALL emit
diagnostic output. Silence during a multi-second stall is itself a defect: the production
incident this change addresses produced 57 s of stall with no log line, span, or metric.

#### Scenario: A stalled probe is logged and measured

- **WHEN** a presence probe exceeds its deadline
- **THEN** a log record MUST be emitted identifying the NAR and the elapsed time
- **AND** a metric counting probe timeouts MUST be incremented
- **AND** the tracing span for the probe MUST record the timeout

#### Scenario: Probe latency is recorded for successful probes

- **WHEN** a presence probe completes successfully
- **THEN** its duration MUST be recorded so slow-but-successful probes are visible before they
  become timeouts
