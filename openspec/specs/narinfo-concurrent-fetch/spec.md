# narinfo-concurrent-fetch

## Purpose

Defines the correctness requirements for serving narinfo requests when multiple
concurrent requests arrive for the same hash before the upstream fetch has
completed. The core invariant is that no concurrent request may receive a 404
caused by the purge guard firing while a NAR download job is still in-flight.

## Requirements

### Requirement: Concurrent narinfo requests for the same hash must not produce 404 responses

When multiple requests arrive concurrently for a narinfo hash that is being fetched from upstream for the first time, all requests must eventually receive the narinfo (or a valid error), and none must receive a 404 caused by a spurious purge of the narinfo from the database.

#### Scenario: Two concurrent requests for the same narinfo hash — second request must not get 404

**Given** a cold cache (narinfo not yet stored locally)
**And** a valid upstream narinfo for hash `H` exists
**When** two requests for hash `H` arrive concurrently such that the second request calls `getNarInfoFromDatabase` while the NAR download goroutine for `H` is still running
**Then** both requests receive the narinfo successfully
**And** neither request receives a 404 or `storage.ErrNotFound`

#### Scenario: Three concurrent requests for the same narinfo hash — all must succeed

**Given** a cold cache
**And** a valid upstream narinfo for hash `H` (with `Compression: none` and NAR size > 100KB) exists
**When** three requests for hash `H` arrive in parallel (e.g. from `t.Parallel()` subtests sharing the same server)
**Then** all three requests receive the narinfo successfully with HTTP 200
**And** the narinfo is stored exactly once in the database
**And** no spurious purge of the narinfo occurs

#### Scenario: Purge guard skips purge when NAR download job is in-flight

**Given** narinfo for hash `H` is stored in the database
**And** a NAR download job for hash `H` is currently registered in `upstreamJobs`
**When** `getNarInfoFromDatabase` is called and `HasNarInStore` returns false
**Then** `getNarInfoFromDatabase` does NOT purge the narinfo from the database
**And** it returns the narinfo to the caller
**And** no `errNarInfoPurged` is triggered

#### Scenario: Purge guard still fires when NAR is genuinely missing and no job is in-flight

**Given** narinfo for hash `H` is stored in the database
**And** the NAR file for hash `H` is absent from the store
**And** no upstream job (`narInfoJobKey` or `narJobKey`) for hash `H` is registered
**And** no remote download is in progress for hash `H`
**When** `getNarInfoFromDatabase` is called
**Then** the narinfo IS purged from the database
**And** `errNarInfoPurged` is returned (triggering a fresh upstream fetch on the next request)

### Requirement: The fix must not affect the common (cache-warm) case latency

**Given** narinfo for hash `H` is already stored in the database and NAR is already in the store
**When** a request for hash `H` arrives
**Then** `getNarInfoFromDatabase` returns the narinfo directly without any additional synchronization overhead
**And** no upstream fetch is triggered

### Requirement: The fix must not alter behavior for single-hash sequential requests

**Given** narinfo for hash `H` is fetched by a single request (no concurrency)
**When** the fetch completes (narinfo stored, NAR downloaded)
**And** subsequent requests arrive after the job has been removed from `upstreamJobs`
**Then** `getNarInfoFromDatabase` behaves identically to before the fix
**And** `HasNarInStore` returns true (NAR is in store) so the purge guard is not triggered

### Requirement: A lock-losing replica MUST NOT return HTTP 500 from narinfo coordination

A lock-losing replica MUST NOT return HTTP 500 from narinfo coordination. When a replica fails to acquire the distributed download lock for a narinfo hash (`waitForStorage=true` coordination, because another replica holds it), it SHALL serve the narinfo (if the holder stores it), take over the fetch (if the holder finishes without storing it), or return a clean cache miss (HTTP 404) if the narinfo is genuinely unavailable upstream.

#### Scenario: Second concurrent narinfo request loses the lock and the holder times out

- **WHEN** two replicas request narinfo for hash `H` concurrently against a cold cache
- **AND** replica B fails to acquire the lock held by replica A
- **AND** replica A does not store the narinfo within the previous fixed poll timeout
- **THEN** replica B SHALL NOT return HTTP 500
- **AND** replica B SHALL serve the narinfo once stored, take over the fetch after the lock frees, or return HTTP 404 on terminal give-up

#### Scenario: Holder stores narinfo — waiter serves it

- **WHEN** replica B fails to acquire the narinfo lock for hash `H` held by replica A
- **AND** replica A successfully fetches and stores the narinfo for `H`
- **THEN** replica B SHALL serve the narinfo with HTTP 200
- **AND** replica B SHALL NOT return HTTP 500

#### Scenario: Narinfo genuinely absent upstream — waiter returns 404 not 500

- **WHEN** replica B fails to acquire the narinfo lock for hash `H`
- **AND** the narinfo for `H` does not exist upstream
- **THEN** the coordination path SHALL surface `storage.ErrNotFound`
- **AND** the server SHALL return HTTP 404, not HTTP 500

### Requirement: narinfo lock-loss fallback MUST serialize the upstream fetch

A replica that loses the narinfo download lock SHALL NOT start its own concurrent
upstream narinfo fetch and database write while another replica still holds the
lock; it SHALL take over only after re-acquiring the lock, preserving the
existing exactly-once storage invariant.

#### Scenario: Storage remains exactly-once under lock-loss take-over

- **WHEN** replica B takes over a narinfo fetch for hash `H` after replica A released the lock without storing it
- **THEN** replica B SHALL store the narinfo
- **AND** the narinfo for `H` SHALL be stored exactly once (idempotent upsert), with no duplicate-key error surfaced to the client
