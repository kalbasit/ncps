## ADDED Requirements

### Requirement: A fired narinfo purge MUST NOT surface to the client as HTTP 500

The system SHALL NOT return `HTTP 500 Internal Server Error` to a client as a
result of the narinfo purge guard firing. When `GetNarInfo` determines a narinfo
exists in the database but its backing NAR is absent from storage and no download
job is in flight (neither local nor remote, and no in-progress CDC record), the
purge guard purges the narinfo and internally signals `errNarInfoPurged`. This
sentinel is an internal cache-maintenance signal and MUST NOT propagate to the
HTTP client. The request that triggered the purge SHALL instead resolve to a
served narinfo (HTTP 200) or a not-found response (HTTP 404), never a 500.

#### Scenario: Purged narinfo still available upstream is served, not 500'd

- **WHEN** a client requests `GET /{hash}.narinfo` for a hash whose narinfo is in
  the database but whose NAR is missing from storage and no download is in flight
- **AND** the narinfo is still available from a configured upstream cache
- **THEN** the purge guard fires and purges the stale database record
- **AND** `GetNarInfo` re-fetches the narinfo from upstream
- **AND** the client receives `HTTP 200` with a valid signed narinfo
- **AND** the client never receives `HTTP 500` or the body `the narinfo was purged`

#### Scenario: Purged narinfo absent upstream resolves to 404 fallback

- **WHEN** a client requests `GET /{hash}.narinfo` for a hash whose narinfo is in
  the database but whose NAR is missing from storage and no download is in flight
- **AND** the narinfo is not available from any configured upstream cache
- **THEN** the purge guard fires and purges the stale database record
- **AND** `GetNarInfo` resolves to `storage.ErrNotFound`
- **AND** the client receives `HTTP 404` so Nix falls back to its next substituter
- **AND** the client never receives `HTTP 500`

### Requirement: `GetNarInfo` MUST re-fetch from upstream when the purge guard fires

When the purge guard fires during the initial database lookup, `GetNarInfo` SHALL
treat the purge as a cache miss and proceed through the existing upstream-fetch
path (the same path taken when a narinfo is absent from both database and store),
rather than returning the `errNarInfoPurged` sentinel to its caller. The upstream
fetch outcome — success or `storage.ErrNotFound` — determines the served result.

#### Scenario: Purge during database lookup falls through to upstream fetch

- **WHEN** `getNarInfoFromDatabase` returns `errNarInfoPurged` for a hash
- **THEN** `GetNarInfo` does not return that sentinel to its caller
- **AND** `GetNarInfo` initiates an upstream narinfo fetch for the hash
- **AND** the final result is the narinfo (on upstream success) or
  `storage.ErrNotFound` (on upstream miss)

#### Scenario: Re-fetch failure surfaces the upstream error, not the purge sentinel

- **WHEN** the purge guard fires and the subsequent upstream fetch fails with a
  transient error (e.g. a network error, not `storage.ErrNotFound`)
- **THEN** `GetNarInfo` returns the upstream fetch error
- **AND** the returned error is never `errNarInfoPurged`
- **AND** a later request for the same hash is able to re-attempt the upstream fetch

### Requirement: The narinfo HTTP handler MUST map a leaked purge sentinel to 404

The narinfo `GET` handler in `pkg/server` SHALL, as defense in depth, treat
`errNarInfoPurged` equivalently to `storage.ErrNotFound` and respond with
`HTTP 404` if the sentinel ever reaches it, never `HTTP 500`. The handler MUST NOT
write the internal sentinel message into any client response body.

#### Scenario: Handler receives the purge sentinel

- **WHEN** the narinfo `GET` handler receives `errNarInfoPurged` from `GetNarInfo`
- **THEN** the handler responds with `HTTP 404 Not Found`
- **AND** the response body does not contain `the narinfo was purged`
