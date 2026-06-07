## ADDED Requirements

### Requirement: A NAR HEAD/existence probe MUST reflect actual servability, not a bare nar_file record

A NAR `HEAD` request (`HEAD /nar/<hash>.nar[.<compression>]`, including under `/upload`) MUST NOT report the NAR as present (HTTP 200) on the basis of the `nar_file` database record alone. The system SHALL apply the same servability determination used by `GetNar`: a NAR is "servable" only when a whole-file exists in the store, `total_chunks > 0`, or chunking is actively in progress within the lock TTL. A `nar_file` row's existence or its recorded `FileSize` (e.g. via a `GetNarFileSize`/`HasNarFileRecord`-style lookup) SHALL NOT by itself produce a `200`.

When the NAR is not servable (a record exists but no backing bytes), the `HEAD` outcome MUST be consistent with `GetNar`:

- On the upload path (`cache.IsUploadOnly`), `HEAD` SHALL resolve to `storage.ErrNotFound` (HTTP 404) so the client re-uploads the NAR.
- On the substituter path, `HEAD` SHALL follow the same recovery path as `GetNar` (attempt upstream recovery) rather than returning a bare record-based `200`, and SHALL ultimately reflect whether the NAR could be made servable.

The check MUST honor the tri-state storage stat: an ambiguous or transient storage error (not a confirmed absence) MUST NOT be turned into a false `200` nor a false `404`.

#### Scenario: Upload-only HEAD of a backing-less NAR returns 404

- **GIVEN** a `nar_file` row for hash `H` exists with a recorded `FileSize > 0`
- **AND** neither a whole-file nor any chunks for `H` exist in storage, and chunking is not in progress
- **WHEN** a client sends `HEAD /upload/nar/<H>.nar.zst`
- **THEN** the system SHALL respond `404` (resolving to `storage.ErrNotFound`)
- **AND** SHALL NOT respond `200` on the basis of the `nar_file` record or its `FileSize`

#### Scenario: HEAD of a servable NAR returns 200

- **GIVEN** a `nar_file` row for hash `H` whose backing bytes are present in the store (whole-file or `total_chunks > 0`)
- **WHEN** a client sends `HEAD /nar/<H>.nar.zst` (or under `/upload`)
- **THEN** the system SHALL respond `200`

#### Scenario: nix re-uploads a NAR whose bytes are missing

- **GIVEN** ncps holds a `nar_file` record for `H` with no backing bytes
- **WHEN** `nix copy --to <host>/upload` evaluates `H` and sends `HEAD /upload/nar/<H>.nar.zst`
- **THEN** the `404` response causes nix to upload the NAR bytes for `H` (`PUT /upload/nar/<H>.nar.zst`)
- **AND** the path does not remain a phantom that fails a later reference-verification `GET`

#### Scenario: Ambiguous storage error on HEAD is not a false present/absent

- **GIVEN** a `nar_file` row for hash `H`
- **AND** a storage stat for `H` fails transiently (timeout / stale-metadata read) rather than returning a definite not-found
- **WHEN** the system evaluates NAR presence for a `HEAD` request
- **THEN** it SHALL NOT report `200` solely from the record, and SHALL NOT treat the ambiguous error as a confirmed absence
