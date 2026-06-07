## ADDED Requirements

### Requirement: Read path MUST prefer in-flight staging over progressive chunk streaming during the chunking window

During the eager-CDC chunking window (`total_chunks == 0`), when in-flight staging parts are available for the NAR hash, the read path SHALL serve from the staging parts in preference to `streamProgressiveChunks`. Progressive chunk streaming SHALL remain the fallback used only when no staging parts are present. This precedence SHALL NOT change steady-state serving once `total_chunks > 0`.

#### Scenario: Staging present during chunking — prefer staging

- **WHEN** a NAR hash has `total_chunks == 0` (actively chunking)
- **AND** in-flight staging parts for that hash are available in shared storage
- **THEN** the read path SHALL serve from the staging parts
- **AND** it SHALL NOT enter `streamProgressiveChunks` for that request

#### Scenario: No staging present during chunking — fall back to progressive chunks

- **WHEN** a NAR hash has `total_chunks == 0`
- **AND** no in-flight staging parts are available (feature disabled, uncontended, or already reclaimed)
- **THEN** the read path SHALL fall back to `streamProgressiveChunks` as before

#### Scenario: Steady state unchanged

- **WHEN** a NAR hash has `total_chunks > 0`
- **THEN** serving SHALL proceed from chunks as before, regardless of any staging state
