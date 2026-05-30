## ADDED Requirements

### Requirement: Unrecoverable backing-less placeholder rows MUST be garbage-collected

The recovery process SHALL garbage-collect a backing-less placeholder `nar_file` row —
`total_chunks = 0`, `chunking_started_at` NULL, no whole-file in the store — once it is
**provably unrecoverable**, removing the row together with its `narinfo_nar_files` link,
so such rows do not accumulate in the database or get re-scanned by the CDC lazy-recovery
sweep indefinitely.

"Provably unrecoverable" means the NAR is confirmed genuinely absent upstream (a
definitive not-found, not a timeout or transient failure). A row SHALL NOT be removed
while the NAR can still be served by an upstream: `GetNar` MUST remain able to re-create
the placeholder and download the NAR on demand after collection. A transient or timeout
upstream failure SHALL NOT be treated as genuine absence.

Collection SHALL be bounded (rate-limited by the recovery interval and batch size) and
SHALL only consider rows older than the recovery cutoff, so it cannot race a freshly
created placeholder for an in-flight download.

#### Scenario: Genuinely-absent placeholder is collected

- **GIVEN** a backing-less placeholder `nar_file` row for hash `H` older than the recovery cutoff
- **AND** no whole-file for `H` exists in the store
- **AND** the upstream returns a definitive not-found for `H`
- **WHEN** the recovery process evaluates `H`
- **THEN** the placeholder row and its `narinfo_nar_files` link SHALL be removed
- **AND** the removal SHALL leave no dangling foreign-key reference

#### Scenario: Placeholder whose NAR upstream still has is NOT collected

- **GIVEN** a backing-less placeholder `nar_file` row for hash `H`
- **AND** an upstream still has the NAR for `H`
- **WHEN** the recovery process evaluates `H`
- **THEN** the placeholder row SHALL NOT be removed
- **AND** a later `GetNar` for `H` SHALL re-download and serve it

#### Scenario: Transient upstream failure does not trigger collection

- **GIVEN** a backing-less placeholder `nar_file` row for hash `H`
- **AND** the upstream existence check fails transiently (timeout / connection reset)
- **WHEN** the recovery process evaluates `H`
- **THEN** the placeholder row SHALL NOT be removed
- **AND** `H` SHALL remain eligible for a future re-evaluation
