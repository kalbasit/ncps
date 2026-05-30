## ADDED Requirements

### Requirement: The chunk-wait deadline MUST be bounded and configurable

The serving path SHALL bound the time it waits on chunk production and chunk reads by an explicit, operator-configurable deadline that defaults to a value fitting within a typical reverse-proxy gateway timeout. It SHALL NOT wait indefinitely (or in unbounded per-chunk increments) such that total serving time can exceed the gateway timeout and surface to the client as a gateway 504.

#### Scenario: Per-request serving time is bounded below the gateway timeout

- **GIVEN** a NAR is being served and a chunk read stalls
- **AND** the configured serving deadline is shorter than the gateway timeout
- **WHEN** the serving deadline elapses
- **THEN** the system SHALL terminate the request with a retryable error
- **AND** SHALL NOT allow the request to hang until the gateway returns a 504

#### Scenario: The deadline is configurable

- **GIVEN** an operator sets the chunk-wait / serving deadline via configuration
- **WHEN** the server loads its configuration
- **THEN** the serving path SHALL honor the configured deadline
- **AND** in the absence of explicit configuration SHALL apply a documented default

### Requirement: A chunk stall after the response is committed MUST surface as a stream error, not a clean EOF

The streaming path SHALL terminate a post-commit chunk stall in a way the client observes as a failed/aborted transfer (an abnormal stream/connection termination), never as a clean end-of-body that a client could mistake for a complete NAR. This applies once the response status and some body bytes have already been committed; a short body that decodes as a truncated archive is a forbidden outcome.

#### Scenario: Mid-stream stall aborts the transfer rather than ending it cleanly

- **GIVEN** a NAR for hash `H` is mid-transfer from chunks and bytes are already committed
- **AND** the next required chunk does not arrive within the serving deadline
- **WHEN** the wait elapses
- **THEN** the system SHALL abort the response so the client sees a failed transfer
- **AND** SHALL NOT close the body at the truncation point as if the NAR were complete
- **AND** the client SHALL be free to retry the request
