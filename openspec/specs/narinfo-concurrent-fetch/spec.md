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
