# Proposal: migration-job-needs-no-storage

## Why

The migration Job unconditionally mounts the storage PVC and a large in-memory
`tmp` emptyDir, but `ncps migrate up` only connects to the database — it never
reads or writes to either volume. This couples the PreSync hook to storage
availability and wastes 8 GiB of memory reservation for a job that needs
neither.

## What Changes

- **Helm chart `migration-job.yaml`**: Remove the `tmp` volume and volumeMount
  from the migration container entirely.
- **Helm chart `migration-job.yaml`**: Scope the `storage` volume and
  volumeMount to `database.type == "sqlite"` only (SQLite needs the storage
  path for its database file; non-SQLite databases do not).
- **Helm chart `migration-job.yaml`**: Same narrower condition for the
  `create-db-dir` initContainer's storage volumeMount (already sqlite-gated at
  the container level, but the mount condition inside it uses the broader
  `or local sqlite` check).
- **Helm chart tests**: Update or add unit tests to assert the migration Job
  produced for a non-SQLite backend has no storage or tmp volumes.

## Capabilities

No capability-level (spec) changes — this is a pure Helm chart rendering fix.
No new or modified behavioral specs are needed.

## Impact

- `charts/ncps/templates/migration-job.yaml` — primary change.
- `charts/ncps/tests/migration_test.yaml` — test updates.
- Deployed migration Jobs for PostgreSQL/MySQL users will no longer mount the
  storage PVC or the memory-backed emptyDir, reducing resource reservations and
  removing an unnecessary dependency on PVC availability at sync time.
