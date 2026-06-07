## ADDED Requirements

### Requirement: GetNar reports an unknown size while live-streaming an in-flight download

`Cache.GetNar` returns a size of `-1` when it serves a NAR via the per-client live-streaming path — the path taken when the artifact is not yet fully available locally and an upstream download is still in flight. In that case the final size is not yet known and `-1` SHALL be returned as the "size unknown" sentinel; the NAR's bytes are delivered through the returned reader regardless. When the NAR is instead served from store or from completed chunks, `GetNar` SHALL return the concrete `nar_files.file_size`. Consumers and tests MUST treat `-1` as "size unknown" and MUST NOT assume the concrete size on the streaming path; the streamed bytes are the authoritative measure of size and content.

#### Scenario: Size is -1 when serving an in-flight download

- **WHEN** `GetNar` serves a NAR whose upstream download is still in progress (the live-streaming path)
- **THEN** the returned size SHALL be `-1`
- **AND** the returned reader SHALL still yield the complete, correct NAR bytes

#### Scenario: Concrete size when serving a fully-available NAR

- **WHEN** `GetNar` serves a NAR that is fully present in store or as completed chunks
- **THEN** the returned size SHALL equal the `nar_files.file_size` record
- **AND** the returned reader SHALL yield the complete, correct NAR bytes

#### Scenario: Correctness assertions tolerate either size form

- **WHEN** a caller validates a `GetNar` result without knowing which path served it
- **THEN** it SHALL accept either the concrete `file_size` or `-1` for the size
- **AND** it SHALL verify content by comparing the streamed bytes, which are correct on both paths
