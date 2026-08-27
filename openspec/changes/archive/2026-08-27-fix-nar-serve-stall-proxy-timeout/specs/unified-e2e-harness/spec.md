## ADDED Requirements

### Requirement: NAR read latency assertion

The harness SHALL assert **time-to-first-byte** on NAR reads, not only byte-correctness, in the
serve phase that every serving scenario runs. A response that is byte-exact but arrives too
slowly to survive a reverse proxy's read timeout MUST be reported as a FAILURE.

The assertion SHALL cover a **warm** read — a NAR already present in storage — because that is
the path that failed in production: the NAR was present and every byte was correct, and only the
latency was wrong.

The scenario SHALL measure the interval between issuing the NAR request and receiving the first
body byte, and SHALL fail when that interval exceeds a declared budget. The budget SHALL be far
below any per-request read timeout used by the harness client, so that a stall is caught by the
assertion rather than by a client timeout.

This closes the gap that allowed the production stall to pass every existing scenario: the
in-flight staging contention scenario reads NARs with a 900 s client timeout and asserts only
that the bytes match, so a 57 s stall scored as a PASS.

#### Scenario: Slow first byte fails the scenario

- **WHEN** the harness requests a NAR already present in storage
- **AND** the first body byte arrives later than the declared time-to-first-byte budget
- **THEN** the scenario MUST report FAIL
- **AND** the failure message MUST report the measured time-to-first-byte and the budget

#### Scenario: Byte-correct but slow is not a pass

- **WHEN** a NAR response is byte-identical to the canonical NAR
- **AND** its time-to-first-byte exceeds the budget
- **THEN** the scenario MUST report FAIL, not PASS

#### Scenario: Fast and correct passes

- **WHEN** a NAR response is byte-identical to the canonical NAR
- **AND** its time-to-first-byte is within the budget
- **THEN** the scenario MUST report PASS

#### Scenario: Client timeout is not the assertion mechanism

- **WHEN** the harness measures time-to-first-byte
- **THEN** the per-request client timeout MUST be larger than the budget
- **AND** a stall MUST be reported as a budget violation with a measured value, never as an opaque client timeout
