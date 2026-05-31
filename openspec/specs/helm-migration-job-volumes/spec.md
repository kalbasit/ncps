# Spec: helm-migration-job-volumes

## Purpose

Defines which volumes and volumeMounts the Helm-rendered migration Job includes
depending on storage backend and database type. The migration Job (`ncps migrate
up`) performs no filesystem I/O that requires a scratch directory, and only
needs storage access when using SQLite (which persists its database file on the
storage path).

## Requirements

### Requirement: Migration Job omits tmp volume

The migration Job MUST NOT include a `tmp` volume or volumeMount regardless of
storage backend or database type, because `ncps migrate up` performs no
filesystem I/O that requires a scratch directory.

#### Scenario: PostgreSQL database with local storage

- **WHEN** `config.database.type` is `postgresql` and `config.storage.type` is `local`
- **THEN** the migration Job spec SHALL contain no volume named `tmp` and no volumeMount with `mountPath` matching the cache temp path

#### Scenario: SQLite database with local storage

- **WHEN** `config.database.type` is `sqlite` and `config.storage.type` is `local`
- **THEN** the migration Job spec SHALL contain no volume named `tmp` and no volumeMount with `mountPath` matching the cache temp path

### Requirement: Migration Job mounts storage only for SQLite

The migration Job MUST mount the storage volume only when `config.database.type`
is `sqlite`, because SQLite persists its database file on the storage path.
Non-SQLite databases (PostgreSQL, MySQL) access their data over the network and
require no storage mount.

#### Scenario: PostgreSQL database — no storage volume

- **WHEN** `config.database.type` is `postgresql`
- **THEN** the migration Job spec SHALL contain no volume named `storage` and no volumeMount at `/storage`

#### Scenario: MySQL database — no storage volume

- **WHEN** `config.database.type` is `mysql`
- **THEN** the migration Job spec SHALL contain no volume named `storage` and no volumeMount at `/storage`

#### Scenario: SQLite database — storage volume present

- **WHEN** `config.database.type` is `sqlite`
- **THEN** the migration Job spec SHALL include a volume named `storage` bound to the configured PVC, and the initContainer SHALL mount it at `/storage` to create the database directory

### Requirement: The `create-db-dir` init container security context MUST be operator-configurable

The `create-db-dir` busybox init container SHALL derive its securityContext from `initImage.securityContext` deep-merged with `containerDefaults.securityContext`, with `initImage.securityContext` taking priority. No UID, GID, or any other security field SHALL be hardcoded in the template. When both values are empty, no `securityContext:` key is rendered and the container inherits from `podSecurityContext`.

#### Scenario: Init container uses containerDefaults when initImage.securityContext is empty

- **WHEN** `containerDefaults.securityContext` is set and `initImage.securityContext` is empty
- **THEN** the `create-db-dir` container's rendered securityContext SHALL match `containerDefaults.securityContext`

#### Scenario: initImage.securityContext overrides containerDefaults for conflicting keys

- **WHEN** `containerDefaults.securityContext.runAsUser: 1000` and `initImage.securityContext.runAsUser: 2000`
- **THEN** the `create-db-dir` container's rendered securityContext SHALL have `runAsUser: 2000`

#### Scenario: Changing podSecurityContext.runAsUser propagates to create-db-dir

- **WHEN** `podSecurityContext.runAsUser: 2000` is set and `initImage.securityContext` and `containerDefaults.securityContext` are both empty
- **THEN** the `create-db-dir` container SHALL run as UID 2000 (inherited from pod level, no container-level override)
- **AND** the rendered container manifest SHALL NOT contain a `securityContext:` key
