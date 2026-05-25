# Build Trace Specification

## Purpose

ncps supports the Nix `build-trace-v2` binary cache protocol, which allows clients to record and retrieve build trace entries. A build trace entry is a JSON document recording that building a specific derivation output produced a specific store path, signed by one or more parties. ncps stores each entry in the database as structured columns and appends its own Ed25519 signature on ingestion.

## Requirements

### Requirement: Build trace entry storage and retrieval

The system SHALL store and serve build trace entries for the nix `build-trace-v2` binary cache protocol. A build trace entry is a JSON document recording that building a specific derivation output produced a specific store path, signed by one or more parties. ncps SHALL store each entry in the database as structured columns (`drv_path`, `output_name`, `out_path`) plus a verbatim `raw_json` copy of the upload body.

#### Scenario: Successful PUT
- **WHEN** a client sends `PUT /upload/build-trace-v2/{drvName}/{outputName}.doi` with a valid JSON body and `putPermitted == true`
- **THEN** the system SHALL parse the entry, append ncps's own signature, persist to `build_trace_entries` + `build_trace_signatures`, and return `204 No Content`

#### Scenario: Duplicate PUT (same key, same out_path)
- **WHEN** a client PUTs a build trace entry for a `(drv_path, output_name)` pair that already exists with the same `out_path`
- **THEN** the system SHALL upsert the entry (replacing signatures) and return `204 No Content`

#### Scenario: PUT not permitted
- **WHEN** a client sends `PUT /upload/build-trace-v2/…` and `putPermitted == false`
- **THEN** the system SHALL return `405 Method Not Allowed` without reading the body

#### Scenario: Malformed JSON body
- **WHEN** a client PUTs a body that is not valid JSON or is missing required fields (`key.drvPath`, `key.outputName`, `value.outPath`)
- **THEN** the system SHALL return `400 Bad Request`

#### Scenario: URL/body mismatch
- **WHEN** the `drvName` in the URL does not match `key.drvPath` in the body, or `outputName` does not match `key.outputName`
- **THEN** the system SHALL return `400 Bad Request`

### Requirement: Build trace GET and HEAD

The system SHALL serve stored build trace entries via GET and HEAD, with ncps's own signature included in the response.

#### Scenario: Successful GET
- **WHEN** a client sends `GET /build-trace-v2/{drvName}/{outputName}.doi` and the entry exists
- **THEN** the system SHALL return `200 OK` with `Content-Type: application/json` and a JSON body reconstructed from structured DB columns and all stored signatures (including ncps's own signature appended at PUT time)

#### Scenario: HEAD existence check
- **WHEN** a client sends `HEAD /build-trace-v2/{drvName}/{outputName}.doi` and the entry exists
- **THEN** the system SHALL return `200 OK` with no body

#### Scenario: Entry not found
- **WHEN** a client sends `GET` or `HEAD` for a `(drvName, outputName)` that has not been stored
- **THEN** the system SHALL return `404 Not Found`

### Requirement: Build trace signing

On ingestion, ncps SHALL append its own Ed25519 signature to the build trace entry. The fingerprint signed SHALL be the JSON representation of the full entry (`key` + `value`) with the `signatures` field removed from `value` — matching the nix reference implementation. Existing signatures from the upload body SHALL be preserved alongside ncps's own signature.

#### Scenario: ncps signature appended on PUT
- **WHEN** a valid build trace entry is stored
- **THEN** `build_trace_signatures` SHALL contain at least one row with `key_name` equal to ncps's hostname and a valid Ed25519 signature over the entry's fingerprint

#### Scenario: Upstream signatures preserved
- **WHEN** the uploaded entry contains existing signatures
- **THEN** those signatures SHALL also be stored in `build_trace_signatures` and appear in GET responses

### Requirement: Build trace URL parsing

The route path `build-trace-v2/{drvName}/{outputName}.doi` SHALL correctly extract the derivation filename and output name. The `.doi` suffix SHALL be stripped from `outputName` by the handler before any lookup or storage operation.

#### Scenario: Standard path parsing
- **WHEN** the URL is `/build-trace-v2/qwwz2sxy84n4slkyff4jirbihqk3qvhf-skopeo-1.21.0.drv/out.doi`
- **THEN** `drvName` SHALL be `qwwz2sxy84n4slkyff4jirbihqk3qvhf-skopeo-1.21.0.drv` and `outputName` SHALL be `out`

#### Scenario: Non-default output name
- **WHEN** the URL ends in `/dev.doi`
- **THEN** `outputName` SHALL be `dev`
