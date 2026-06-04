## ADDED Requirements

### Requirement: Upload-only narinfo reads MUST NOT purge on a missing-NAR cache miss

Upload-only narinfo reads MUST NOT trigger a destructive purge of the narinfo or its `nar_file` records. When the purge guard's condition is met (a narinfo exists in the database but its backing NAR is absent from storage and no download is in flight — neither local nor remote, and no in-progress CDC record) **and** the request is upload-only (`cache.IsUploadOnly(ctx)` is true, i.e. the request arrived on the `/upload` route), `GetNarInfo` SHALL NOT call `purgeNarInfo` and SHALL NOT delete the narinfo or `nar_file` database records. It SHALL resolve the read as a cache miss by returning `storage.ErrNotFound`, and SHALL NOT attempt an upstream fetch. The client receives `HTTP 404` and proceeds to re-`PUT` the narinfo, whose write overwrites the stale record.

This keeps upload-path reads non-destructive and idempotent: the cache's answer to
"is this path present?" stays stable (monotonic) across repeated reads within a
single `nix copy`, which the client's reference-verification step relies on. The
non-upload (root/substituter) purge-and-re-fetch behavior is unaffected.

#### Scenario: Upload-only read of a missing-NAR narinfo returns 404 without purging

- **WHEN** a client requests `GET /upload/{hash}.narinfo` for a hash whose narinfo
  is in the database but whose NAR is missing from storage and no download is in
  flight
- **THEN** the purge guard's missing-NAR condition is met, but `purgeNarInfo` is NOT called
- **AND** the narinfo and `nar_file` database records remain present
- **AND** `GetNarInfo` returns `storage.ErrNotFound`
- **AND** the client receives `HTTP 404`
- **AND** no upstream narinfo or NAR fetch is attempted

#### Scenario: Repeated upload-only reads are deterministic and non-mutating

- **WHEN** a client issues the same `GET /upload/{hash}.narinfo` for a missing-NAR
  narinfo two or more times in succession
- **THEN** every response is `HTTP 404`
- **AND** no read mutates the database (the narinfo and `nar_file` records are
  identical before and after each read)

#### Scenario: A subsequent upload PUT repairs the stale record

- **WHEN** an upload-only read has returned `HTTP 404` for a missing-NAR narinfo
  without purging
- **AND** the client then `PUT`s the NAR bytes followed by the narinfo
- **THEN** the narinfo PUT overwrites/repairs the stale record
- **AND** a later read for the same hash serves the narinfo (`HTTP 200`)

## MODIFIED Requirements

### Requirement: `GetNarInfo` MUST re-fetch from upstream when the purge guard fires

For requests that are not upload-only, `GetNarInfo` SHALL treat a fired purge guard during the initial database lookup as a cache miss and proceed through the existing upstream-fetch path (the same path taken when a narinfo is absent from both database and store), rather than returning the `errNarInfoPurged` sentinel to its caller. The upstream fetch outcome — success or `storage.ErrNotFound` — determines the served result. For upload-only requests the purge guard does not fire and no upstream fetch is attempted; see "Upload-only narinfo reads MUST NOT purge on a missing-NAR cache miss".

#### Scenario: Purge during database lookup falls through to upstream fetch

- **WHEN** `getNarInfoFromDatabase` returns `errNarInfoPurged` for a hash on a
  non-upload-only request
- **THEN** `GetNarInfo` does not return that sentinel to its caller
- **AND** `GetNarInfo` initiates an upstream narinfo fetch for the hash
- **AND** the final result is the narinfo (on upstream success) or
  `storage.ErrNotFound` (on upstream miss)

#### Scenario: Re-fetch failure surfaces the upstream error, not the purge sentinel

- **WHEN** the purge guard fires on a non-upload-only request and the subsequent
  upstream fetch fails with a transient error (e.g. a network error, not
  `storage.ErrNotFound`)
- **THEN** `GetNarInfo` returns the upstream fetch error
- **AND** the returned error is never `errNarInfoPurged`
- **AND** a later request for the same hash is able to re-attempt the upstream fetch
