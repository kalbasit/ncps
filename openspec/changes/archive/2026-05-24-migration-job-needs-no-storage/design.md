# Design: migration-job-needs-no-storage

## Context

`migration-job.yaml` constructs volumes and volumeMounts using the condition:

```
or (eq .Values.config.storage.type "local") (eq .Values.config.database.type "sqlite")
```

This makes the storage PVC present whenever local storage is configured, even
when the database is PostgreSQL or MySQL. The `tmp` emptyDir is always present.
Neither volume is needed by `ncps migrate up`: the command opens a database
connection (from `CACHE_DATABASE_URL`) and runs Goose migrations — it performs
no filesystem I/O outside the database driver.

Stakeholders: users running ArgoCD PreSync migration jobs with non-SQLite
databases and local storage.

## Goals / Non-Goals

**Goals:**
- Remove the `tmp` volume and volumeMount from the migration Job unconditionally.
- Scope the `storage` volume and volumeMount to `database.type == "sqlite"` only.
- Apply the same narrow condition inside the `create-db-dir` initContainer's
  inner volumeMount (currently also guarded by the broader `or local sqlite`).
- Add/update helm-unittest tests to assert the corrected rendering.

**Non-Goals:**
- Changes to Go application code — `ncps migrate up` already works without storage.
- Changes to the main Deployment/StatefulSet volumes.
- Changes to the fsck CronJob or any other workload.

## Decisions

### Decision 1: Remove `tmp` volume entirely from migration Job

The `tmp` volume exists so the main server can write temporary NAR files during
download. The migration command has no download path and will never write to
`/tmp/ncps`. Keeping it adds an 8 GiB memory reservation for zero benefit.

**Alternative considered**: conditionally mount tmp when some future migration
needs scratch space. Rejected: YAGNI. If a future migration needs scratch, it
can re-add the volume at that time.

### Decision 2: Gate `storage` on `database.type == "sqlite"` only

SQLite stores its database file inside the storage path, so the PVC is needed
for the initContainer to create the parent directory. PostgreSQL and MySQL
databases have their own network endpoint — no filesystem path needed.

The current condition also includes `storage.type == "local"`, which is the
trigger for the user-reported bug: a local-storage + postgresql deployment
unnecessarily pulls in the PVC.

**Alternative considered**: add a separate `migration.mountStorage` bool values
override. Rejected: the correct behavior is deterministic from the database
type; an override would just be a footgun.

### Decision 3: No Go code changes

The `migrate up` command is already storage-agnostic. The bug is purely in
how the Helm chart assembles the Pod spec.

## Risks / Trade-offs

[Risk: SQLite + non-local storage] — The `storage` volume condition
`eq .Values.config.database.type "sqlite"` always evaluates to true for SQLite,
regardless of the storage backend, which is correct — SQLite needs a PVC to
persist. No regression.

[Risk: initContainer storage mount] — The `create-db-dir` initContainer is
already inside `{{- if eq .Values.config.database.type "sqlite" }}`, so the
inner mount condition change is redundant but explicit for consistency.
