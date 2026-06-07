# narinfo-purge-serving Specification

## Purpose

Defines how the cache serves narinfo requests when the internal purge guard fires
— a narinfo exists in the database but its backing NAR is absent from storage and
no download is in flight. A purge is internal cache maintenance, not a server
fault, so it MUST NOT surface to clients as an HTTP 500. Instead the cache
re-fetches from upstream and serves HTTP 200 when available, or resolves to HTTP
404 so Nix falls back to its next substituter. This capability complements
`narinfo-concurrent-fetch` (the in-flight concurrent case) and
`nar-cache-miss-recovery` (NAR record handling).
## Requirements
### Requirement: A fired narinfo purge MUST NOT surface to the client as HTTP 500

The system SHALL NOT return `HTTP 500 Internal Server Error` to a client as a
result of the missing-NAR cache-miss guard firing. When `GetNarInfo` determines a
narinfo exists (in the database or narinfo store) but its backing NAR is absent
from storage and no download job is in flight (neither local nor remote, and no
in-progress CDC record), the read path internally signals `errNarInfoPurged`. This
sentinel is an internal cache-maintenance signal and MUST NOT propagate to the
HTTP client. The request SHALL instead resolve to a served narinfo (HTTP 200) or a
not-found response (HTTP 404), never a 500. Detecting the missing NAR MUST NOT
destructively delete the narinfo or `nar_file` records (see "The substituter read
path MUST NOT destructively purge on a missing-NAR cache miss").

#### Scenario: Missing-NAR narinfo still available upstream is served, not 500'd

- **WHEN** a client requests `GET /{hash}.narinfo` for a hash whose narinfo is in
  the database but whose NAR is missing from storage and no download is in flight
- **AND** the narinfo is still available from a configured upstream cache
- **THEN** `GetNarInfo` re-fetches the narinfo and NAR from upstream **without
  first deleting the existing narinfo or `nar_file` records**
- **AND** the client receives `HTTP 200` with a valid signed narinfo
- **AND** the client never receives `HTTP 500` or the body `the narinfo was purged`

#### Scenario: Missing-NAR narinfo absent upstream resolves to 404 fallback

- **WHEN** a client requests `GET /{hash}.narinfo` for a hash whose narinfo is in
  the database but whose NAR is missing from storage and no download is in flight
- **AND** the narinfo is not available from any configured upstream cache
- **THEN** `GetNarInfo` re-attempts the upstream fetch, which misses
- **AND** the request resolves to `storage.ErrNotFound`
- **AND** the client receives `HTTP 404` so Nix falls back to its next substituter
- **AND** the existing narinfo / `nar_file` records are left intact (not deleted),
  so a later upstream availability or upload PUT can heal them
- **AND** the client never receives `HTTP 500`

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

### Requirement: `GetNarInfo` MUST re-fetch from upstream when the purge guard fires

For requests that are not upload-only, `GetNarInfo` SHALL treat a narinfo whose
backing NAR is missing as a cache miss and proceed through the existing
upstream-fetch path (the same path taken when a narinfo is absent from both
database and store), rather than returning the `errNarInfoPurged` sentinel to its
caller. The re-fetch SHALL be **non-destructive**: it MUST NOT delete the narinfo
or `nar_file` records before or instead of re-fetching. The upstream outcome —
success (overwrites/heals the record) or `storage.ErrNotFound` — determines the
served result. For upload-only requests no upstream fetch is attempted; see
"Upload-only narinfo reads MUST NOT purge on a missing-NAR cache miss".

#### Scenario: Missing-NAR detection falls through to upstream fetch without deleting

- **WHEN** `getNarInfoFromDatabase` (or `getNarInfoFromStore`) detects a narinfo
  whose backing NAR is missing on a non-upload-only request
- **THEN** it returns `errNarInfoPurged` **without calling `purgeNarInfo`**
- **AND** `GetNarInfo` does not return that sentinel to its caller
- **AND** `GetNarInfo` initiates an upstream narinfo+NAR fetch for the hash
- **AND** the final result is the narinfo (on upstream success) or
  `storage.ErrNotFound` (on upstream miss)

#### Scenario: Re-fetch failure surfaces the upstream error, not the sentinel

- **WHEN** the missing-NAR guard fires on a non-upload-only request and the
  subsequent upstream fetch fails with a transient error (not `storage.ErrNotFound`)
- **THEN** `GetNarInfo` returns the upstream fetch error
- **AND** the returned error is never `errNarInfoPurged`
- **AND** the narinfo / `nar_file` records remain intact so a later request can
  re-attempt the upstream fetch

### Requirement: The narinfo HTTP handler MUST map a leaked purge sentinel to 404

The narinfo `GET` handler in `pkg/server` SHALL, as defense in depth, treat
`errNarInfoPurged` equivalently to `storage.ErrNotFound` and respond with
`HTTP 404` if the sentinel ever reaches it, never `HTTP 500`. The handler MUST NOT
write the internal sentinel message into any client response body.

#### Scenario: Handler receives the purge sentinel

- **WHEN** the narinfo `GET` handler receives `errNarInfoPurged` from `GetNarInfo`
- **THEN** the handler responds with `HTTP 404 Not Found`
- **AND** the response body does not contain `the narinfo was purged`

### Requirement: The substituter read path MUST NOT destructively purge on a missing-NAR cache miss

The non-upload (root/substituter) narinfo read path MUST NOT call `purgeNarInfo`
or otherwise delete the narinfo or `nar_file` records when its only fault is a
missing backing NAR. It SHALL self-heal by re-fetching from upstream and
overwriting the record in place. This makes the substituter path's path-validity
answer **monotonic within a single `nix copy`**: a reference that reads present
MUST NOT be made absent by a concurrent substituter read of the same hash. This is
critical because production shares one database across replicas, so any delete is
globally visible and can flip a concurrently-verified reference `200 -> 404`,
aborting the client with `cannot add 'X' because the reference 'Y' does not exist`.

#### Scenario: Substituter read of a missing-NAR narinfo does not delete records

- **WHEN** a client requests `GET /{hash}.narinfo` (non-upload) for a hash whose
  narinfo is in the database but whose NAR is missing and no download is in flight
- **THEN** `purgeNarInfo` is NOT called
- **AND** the narinfo and `nar_file` database records are present both before and
  after the read (modulo an upstream re-fetch overwriting them in place)
- **AND** the NAR bytes are never deleted from the store as part of this read

#### Scenario: A concurrent substituter read does not invalidate an in-flight upload's reference

- **WHEN** an upload is in progress and a reference hash `Y` reads present (HTTP
  200) for the upload's validity check
- **AND** concurrently a substituter read of `Y` observes its backing NAR as
  momentarily absent
- **THEN** the substituter read does NOT delete `Y`'s narinfo or `nar_file` records
- **AND** a subsequent reference-verification read of `Y` still observes it present
  (HTTP 200), so the upload is not aborted

### Requirement: Narinfo-driven NAR deletion MUST be refcount-aware

A `nar_file` record or its NAR bytes MUST NOT be deleted by an operation triggered by a single narinfo (e.g. `purgeNarInfo` for a corrupt narinfo) while another narinfo still links to that `nar_file`. This is
because `narinfo`↔`nar_file` is a many-to-many relationship (via `NarInfoNarFile`;
many narinfos may link to one `nar_file`/NAR). Only a truly orphaned `nar_file`
(zero remaining `NarInfoNarFile` links) SHALL have its record and bytes deleted,
mirroring how `RunLRU` already gates `nar_file` deletion on
`Not(HasNarInfoNarFiles())`.

#### Scenario: Purging narinfo A does not delete a NAR still linked to narinfo B

- **WHEN** narinfos `A` and `B` both link to the same `nar_file` `F`
- **AND** a deletion is triggered for narinfo `A` (e.g. `A` is corrupt/unparseable)
- **THEN** narinfo `A`'s record may be removed
- **AND** `nar_file` `F` and its NAR bytes are NOT deleted because `B` still links to `F`
- **AND** a subsequent request for narinfo `B` still serves its NAR

#### Scenario: A truly orphaned NAR may be deleted

- **WHEN** narinfo `A` is the only narinfo linking to `nar_file` `F`
- **AND** a deletion is triggered for narinfo `A`
- **THEN** narinfo `A`'s record is removed
- **AND** `nar_file` `F` (now orphaned, zero links) and its NAR bytes may be deleted

