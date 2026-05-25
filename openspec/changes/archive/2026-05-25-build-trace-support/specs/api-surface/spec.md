## ADDED Requirements

### Requirement: `HEAD /build-trace-v2/{drvName}/{outputName}.doi` endpoint
The system SHALL expose a HEAD endpoint at `/build-trace-v2/{drvName}/{outputName}.doi` that returns `200 OK` if the entry exists or `404 Not Found` if it does not. No body is returned.

#### Scenario: Entry exists
- **WHEN** a client sends `HEAD /build-trace-v2/{drvName}/{outputName}.doi` for a stored entry
- **THEN** the system SHALL return `200 OK` with no body

#### Scenario: Entry does not exist
- **WHEN** a client sends `HEAD /build-trace-v2/{drvName}/{outputName}.doi` for an unknown entry
- **THEN** the system SHALL return `404 Not Found`

### Requirement: `GET /build-trace-v2/{drvName}/{outputName}.doi` endpoint
The system SHALL expose a GET endpoint at `/build-trace-v2/{drvName}/{outputName}.doi` that returns the stored build trace entry as JSON.

**Response:** `Content-Type: application/json`, body is the build trace v3 JSON object with `key` and `value` fields, `value.signatures` containing all stored signatures including ncps's own.

**Failure modes:**
- `404 Not Found` — entry not found.
- `500 Internal Server Error` — database error.

#### Scenario: Successful GET
- **WHEN** a client sends `GET /build-trace-v2/{drvName}/{outputName}.doi` and the entry exists
- **THEN** the system SHALL return `200 OK` with a JSON body containing `key` and `value` fields

#### Scenario: Not found
- **WHEN** a client sends `GET /build-trace-v2/{drvName}/{outputName}.doi` for an unknown entry
- **THEN** the system SHALL return `404 Not Found`

### Requirement: `PUT /upload/build-trace-v2/{drvName}/{outputName}.doi` endpoint
The system SHALL expose a PUT endpoint under the `/upload` prefix for storing build trace entries. Authorization follows the same `putPermitted` boolean gate used by narinfo and NAR uploads.

**Request:** `Content-Type: application/json`, body is a build trace v3 JSON object.

**Response:** `204 No Content` on success. (Consistent with `putNarInfo` and `putNar`.)

**Authorization:** `putPermitted == false` (the default) causes an immediate `405 Method Not Allowed` response before any body is read. No per-request authentication. (Consistent with existing narinfo/NAR upload behavior.)

**Failure modes:**
- `400 Bad Request` — malformed JSON or URL/body mismatch.
- `405 Method Not Allowed` — PUT not permitted.
- `500 Internal Server Error` — database error.

#### Scenario: Successful PUT
- **WHEN** `putPermitted == true` and the client sends a valid build trace JSON body
- **THEN** the system SHALL return `204 No Content`

#### Scenario: PUT not permitted
- **WHEN** `putPermitted == false`
- **THEN** the system SHALL return `405 Method Not Allowed`

#### Scenario: Invalid body
- **WHEN** the body is not valid JSON or missing required fields
- **THEN** the system SHALL return `400 Bad Request`
