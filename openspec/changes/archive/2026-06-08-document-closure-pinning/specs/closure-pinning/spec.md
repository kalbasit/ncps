## ADDED Requirements

### Requirement: Closure pinning is documented in the user guide
The user-facing documentation SHALL include a dedicated page describing the
closure pinning feature so operators can discover and use it. The page MUST
cover the pin, unpin, and list endpoints, their success and error status codes,
the idempotency of pin and unpin, and that pinning protects a narinfo and all
its transitive references from LRU eviction.

#### Scenario: A discoverable feature page exists
- **WHEN** the documentation is published
- **THEN** a page titled "Pinning" exists under the User Guide → Features section
- **AND** it is registered in `docs/!!!meta.json` with a stable `shareAlias` slug so it appears in the navigation tree

#### Scenario: Endpoints are documented to match the implementation
- **WHEN** a reader follows the Pinning page
- **THEN** it documents `POST /pin/{hash}.narinfo` returning `200 OK` on success and `404 Not Found` for an unknown narinfo hash
- **AND** documents `DELETE /pin/{hash}.narinfo` returning `200 OK` (idempotent)
- **AND** documents `GET /pins` returning a JSON array of pinned narinfo hashes with `Content-Type: application/json`

#### Scenario: Eviction-protection semantics are explained
- **WHEN** a reader follows the Pinning page
- **THEN** it states that a pinned closure and all of its transitive references are excluded from LRU eviction
- **AND** it provides at least one worked `curl` example for pinning, unpinning, and listing

#### Scenario: The feature is cross-linked from cache-size guidance
- **WHEN** a reader is on the Cache Management or Getting Started → Concepts page where LRU eviction is described
- **THEN** a link points to the Pinning page as the supported way to protect store paths from eviction
