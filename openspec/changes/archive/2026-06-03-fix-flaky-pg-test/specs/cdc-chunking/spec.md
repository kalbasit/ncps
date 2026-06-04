## ADDED Requirements

### Requirement: Serving a whole-file NAR MUST be resilient to a stale time-of-check store-presence observation

The system SHALL serve a NAR whose whole file is present at serve time even if an
earlier time-of-check observed it absent. `GetNar` computes a store-presence flag
(`hasNarInStore`) once and then re-evaluates servability via `isServable`, which
performs its own fresh `HasNarInStore` check. When the whole file lands in the
store between those two checks, the first flag is stale (`false`) while the NAR is
in fact present and servable. The serve path MUST NOT treat that stale `false` as
authoritative and route an uncompressed request to the chunk store.

The system SHALL guarantee that an uncompressed serve request is routed to the
chunk store ONLY when a chunk store is available. When no chunk store is
configured, the serve path MUST serve from the whole-file store (re-evaluating
store presence as needed) rather than calling the chunk-serve path. It MUST NOT
surface `chunk store not initialized, cannot serve NAR from chunks` for a NAR
whose whole file is present.

`GetNar` MAY only return `storage.ErrNotFound` for such a request when the whole
file is genuinely absent from the store AND (no chunk store is configured OR the
chunks cannot be reassembled). A NAR that is observed servable but whose backing
cannot actually produce bytes MUST fall through to the normal cache-miss recovery
(re-download), not surface a chunk-store-unavailable error.

This requirement is the inverse complement of "Serving a whole-file NAR MUST be
resilient to a concurrent background migration": that requirement covers
present-at-check / deleted-before-use (fall back store→chunks); this one covers
absent-at-check / present-before-use (do not route to chunks; serve the present
whole file). Both eliminate reliance on a single stale `hasInStore` observation.

#### Scenario: Whole-file lands between time-of-check and serve, no chunk store

- **GIVEN** CDC is disabled (no chunk store is configured)
- **AND** a NAR for hash `H` is being downloaded so its whole file is briefly absent from the store
- **WHEN** `GetNar(H)` observes `hasNarInStore = false`, then the whole file lands in the store, and `isServable` subsequently observes the whole file present
- **THEN** `GetNar` serves `H` from the whole-file store and returns the complete NAR bytes with no error
- **AND** the error `chunk store not initialized, cannot serve NAR from chunks` is NOT surfaced

#### Scenario: Uncompressed serve never routes to chunks when no chunk store exists

- **GIVEN** no chunk store is configured
- **WHEN** the serve path handles an uncompressed (`Compression == none`) request whose stale `hasInStore` flag is `false`
- **THEN** the serve path resolves against the whole-file store, not `getNarFromChunks`
- **AND** no `chunk store not initialized` error is produced

#### Scenario: Genuinely absent NAR with no chunk store still recovers via re-download

- **GIVEN** no chunk store is configured
- **AND** a hash `H` whose whole file is genuinely absent from the store but available upstream
- **WHEN** `GetNar(H)` is called (not in upload-only mode)
- **THEN** `GetNar` falls through to the upstream re-download path and serves `H` successfully
- **AND** it does NOT return `chunk store not initialized`

#### Scenario: Genuinely absent NAR in upload-only mode still returns not found

- **GIVEN** no chunk store is configured
- **AND** a hash `H` that has neither a whole file in the store nor any upstream source
- **WHEN** `GetNar(H)` is called in upload-only mode
- **THEN** `GetNar` returns `storage.ErrNotFound`
- **AND** it does NOT return `chunk store not initialized`
