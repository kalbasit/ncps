## MODIFIED Requirements

### Requirement: A timed-out presence probe MUST NOT be reported as asset absence

A presence probe that times out SHALL be treated as an indeterminate result, never as a
determination that the asset is absent. Reporting a stalled probe as absence would silently
convert a slow read into a redundant re-download or a spurious 404.

This SHALL hold regardless of which side observes the expiry first: a backend that honours the
probe deadline and returns its own deadline-exceeded error MUST be classified the same as the
request giving up on the probe.

#### Scenario: Timed-out probe is distinguished from a genuine miss

- **WHEN** the presence probe for a NAR times out
- **THEN** the outcome MUST be classified as indeterminate, distinct from "not present"
- **AND** the request MUST NOT return `404 Not Found` on the strength of the timeout alone

#### Scenario: Backend reports the probe deadline before the request gives up

- **WHEN** the storage backend's presence probe honours the probe deadline
- **AND** the backend returns a deadline-exceeded error before the request stops waiting
- **AND** the request itself has not been cancelled
- **THEN** the outcome MUST be classified as a probe timeout (indeterminate), not as "not present"
- **AND** the timeout MUST be counted and logged like any other probe timeout

#### Scenario: Caller cancellation is not reported as a probe timeout

- **WHEN** the request is cancelled by its caller while the presence probe is in flight
- **THEN** the request MUST report the caller's cancellation, not a probe timeout

#### Scenario: Genuine absence is still reported as absence

- **WHEN** the presence probe completes promptly and reports the asset is not in storage
- **THEN** the existing cache-miss behaviour MUST apply unchanged
