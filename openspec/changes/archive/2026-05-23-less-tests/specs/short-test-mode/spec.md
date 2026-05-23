## MODIFIED Requirements

### Requirement: Identify slow tests via profiling before gating

Before adding or removing `-short` guards, the implementation SHALL profile test execution times against the *current* test suite to identify which tests are slow. The gated set MUST be re-derived after any large-scale restructuring of the suite (such as a `less-tests`-style cleanup), because tests that were once slow may have been removed, consolidated, or had their fixtures hoisted.

#### Scenario: Profiling test execution

- **WHEN** running full test suite with timing
- **THEN** identify tests that take >500ms to complete
- **AND** these tests are candidates for `testing.Short()` gating

#### Scenario: Re-deriving the gated set after a restructuring

- **WHEN** a change has removed, consolidated, or restructured tests that previously carried `testing.Short()` guards
- **THEN** the profiling workflow MUST be re-run on the post-change tree
- **AND** `testing.Short()` guards MUST be added to any newly-slow tests
- **AND** `testing.Short()` guards SHOULD be removed from tests that are no longer slow (e.g., because expensive setup was hoisted)

#### Scenario: A previously-gated test is no longer slow

- **WHEN** post-restructuring profiling shows a previously-gated test now runs in under 500ms
- **THEN** the `testing.Short()` guard MAY be removed from that test
- **AND** the change's `tasks.md` records the removal
