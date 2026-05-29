## ADDED Requirements

### Requirement: The cache layer detects not-found through a single predicate

The cache layer (`pkg/cache`) SHALL classify "database row not found" exclusively through the `database.IsNotFoundError` predicate, rather than calling `ent.IsNotFound` directly. `database.IsNotFoundError` SHALL report true for both Ent's `*NotFoundError` and the package-level `database.ErrNotFound` sentinel, so a single helper governs the cache layer's not-found policy. Package-specific sentinels (`storage.ErrNotFound`, `upstream.ErrNotFound`, `chunk.ErrNotFound`, `config.ErrConfigNotFound`) remain distinct and continue to be matched via `errors.Is`.

#### Scenario: Ent missing-row error is classified as not-found

- **WHEN** a `pkg/cache` query returns Ent's `*NotFoundError`
- **THEN** the cache layer SHALL classify it as not-found via `database.IsNotFoundError`
- **AND** the resulting behavior SHALL be identical to the previous direct `ent.IsNotFound` check

#### Scenario: The not-found sentinel is recognized

- **WHEN** a code path receives `database.ErrNotFound` (e.g. from a test fake or a helper that returns the sentinel)
- **THEN** `database.IsNotFoundError` SHALL report true for it
- **AND** the cache layer SHALL treat it as a missing row rather than an unexpected error

#### Scenario: Unrelated package sentinels are not conflated

- **WHEN** an error is `storage.ErrNotFound`, `upstream.ErrNotFound`, `chunk.ErrNotFound`, or `config.ErrConfigNotFound`
- **THEN** it SHALL continue to be matched by its own `errors.Is` check
- **AND** `database.IsNotFoundError` SHALL NOT be used in place of those package-specific matches
