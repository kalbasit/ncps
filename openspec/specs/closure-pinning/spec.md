# closure-pinning Specification

## Purpose
TBD - created by archiving change pin-closure. Update Purpose after archive.
## Requirements
### Requirement: User can pin a closure
The system SHALL allow users to pin a closure by its narinfo hash. Pinned closures and all their transitive references SHALL be protected from LRU eviction.

#### Scenario: Pin a closure that exists in the database
- **WHEN** user sends `POST /pin/{hash}.narinfo` where the hash exists in the `narinfos` table
- **THEN** the system stores the hash in `pinned_closures` table
- **AND** returns `200 OK`

#### Scenario: Pin a closure that does not exist
- **WHEN** user sends `POST /pin/{hash}.narinfo` where the hash does not exist in the database
- **THEN** the system returns `404 Not Found`

#### Scenario: Pin an already-pinned closure
- **WHEN** user sends `POST /pin/{hash}.narinfo` where the hash is already pinned
- **THEN** the system returns `200 OK` (idempotent)

### Requirement: User can unpin a closure
The system SHALL allow users to unpin a previously pinned closure. After unpinning, the narinfo and its references become eligible for LRU eviction.

#### Scenario: Unpin a pinned closure
- **WHEN** user sends `DELETE /pin/{hash}.narinfo` where the hash is pinned
- **THEN** the system removes the hash from `pinned_closures` table
- **AND** returns `200 OK`

#### Scenario: Unpin a closure that is not pinned
- **WHEN** user sends `DELETE /pin/{hash}.narinfo` where the hash is not pinned
- **THEN** the system returns `200 OK` (idempotent)

### Requirement: User can list pinned closures
The system SHALL allow users to list all pinned closures.

#### Scenario: List pinned closures
- **WHEN** user sends `GET /pins`
- **THEN** the system returns a list of pinned closure hashes
- **AND** returns `200 OK`

#### Scenario: List pinned closures when none exist
- **WHEN** user sends `GET /pins` and no closures are pinned
- **THEN** the system returns an empty list

### Requirement: Pinned closures are protected from LRU eviction
The LRU eviction process SHALL exclude narinfos that are protected by pinned closures from deletion candidates.

#### Scenario: LRU eviction skips pinned closure root
- **WHEN** LRU eviction runs and a pinned narinfo hash is among the least recently used
- **THEN** the system SHALL NOT delete that narinfo
- **AND** SHALL continue to find eligible narinfos until cleanup size is met

#### Scenario: LRU eviction skips transitive references of pinned closures
- **WHEN** LRU eviction runs and a narinfo is a transitive reference of a pinned closure
- **THEN** the system SHALL NOT delete that narinfo
- **AND** SHALL compute the full closure by traversing `narinfo_references` table, resolving each reference to its narinfo via JOIN with the `narinfos` table (note: `narinfo_references.reference` is the full store path base name like `2imigbs1vnh9bdyf42z9mvq23pdshgw4-nghttp2-1.67.1-dev`, not just the hash)

#### Scenario: LRU eviction with deep closure graph
- **WHEN** LRU eviction runs and a pinned closure has many transitive references (depth > 1000)
- **THEN** the system SHALL log a warning
- **AND** SHALL protect all reachable narinfos up to the depth limit

### Requirement: Pin operation fetches missing narinfos from upstream
When pinning a closure, the system SHALL attempt to fetch missing narinfos from upstream caches to ensure the full closure is available.

#### Scenario: Pin closure with missing references in database
- **WHEN** user pins a closure and some references are not in the local database
- **THEN** the system SHALL attempt to fetch those narinfos from upstream caches
- **AND** SHALL pin all successfully fetched narinfos as part of the closure

