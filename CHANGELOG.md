# Changelog

All notable changes to this project will be documented in this file.

## [v0.6.0] - 2026-01-07

##### Highlights

- **High Availability & Distributed Locking**: By combining a shared SQL backend with Redis for distributed locking, you can now run multiple replicas of ncps behind a load balancer for redundancy and zero-downtime deployments.
- **Expanded Database Support**: In addition to SQLite, ncps now supports PostgreSQL and MySQL/MariaDB as metadata storage backends.
- **Analytics Reporting**: Added an analytics system with anonymous reporting capabilities.
- **Performance Improvements**: Implemented sharded locks for local Read/Write operations to reduce contention in single-instance deployments.
- **Database Schema Improvements**: Refactored schema to support many-to-many relationships between NarInfos and NAR files, allowing multiple narinfos to point to one nar.
- **Developer Experience**: Added a unified `run.py` script for local development and a database wrapper generator.

##### Added Features

**Telemetry & Analytics**

- Added upstream cache health metrics to telemetry (configured/healthy counts).
- Added `ncps.upstream_count` attribute to telemetry resources.
- Added database and lock type metrics to OpenTelemetry resources.
- Added anonymous analytics reporting capability to help maintainers understand usage patterns.

**Database & Storage**

- Added a `config` table to the database schema for application settings.
- Added `ON DELETE CASCADE` to foreign key constraints to automatically clean up records when a narinfo is deleted.

**High Availability & Locking**

- Implemented sharded locks for local RWLocker to reduce contention.
- Added configuration for lock retries, TTLs, and a circuit breaker for Redis connections.

##### Bug Fixes

**Core Logic**

- Fixed race conditions in narinfo writes by using hash-specific locks.
- Fixed cache operations to handle `storage.ErrAlreadyExists` gracefully without returning errors.
- Fixed `HasNar` to use HTTP HEAD requests instead of GET for efficiency.

**Deployment & Operations**

- Fixed potential sensitive information leak by excluding command args from telemetry.
- Fixed Helm chart volume mounts for config and signing keys to use `subPath`.
- Fixed MySQL timezone handling by explicitly setting the timezone to UTC.

## [v0.5.2] - 2025-12-31

##### Highlights

- **S3 Compatibility**: Enhanced S3 configuration to support MinIO and other providers requiring path-style access.

##### Added Features

- Added `--cache-storage-s3-force-path-style` flag for S3 storage (required for MinIO).

## [v0.5.1] - 2025-12-29

##### Highlights

- **Upstream Configuration**: Streamlined upstream cache configuration flags and timeouts.

##### Added Features

- Added configurable upstream connection timeouts (`--cache-upstream-dialer-timeout` and `--cache-upstream-response-header-timeout`).

##### Refactoring

- Renamed upstream flags to use the `--cache-upstream-*` prefix (e.g., `--cache-upstream-url`).

## [v0.5.0] - 2025-12-26

##### Highlights

- **S3 Storage Support**: Major release introducing support for S3-compatible storage backends.

##### Added Features

**Storage**

- Added S3-compatible storage backend support (AWS S3, MinIO).
- Added CLI configuration support for S3 storage backend.

**Refactoring & Deprecations**

- Renamed `--cache-data-path` to `--cache-storage-local` (old flag deprecated).

## [v0.4.0] - 2025-10-02

##### Highlights

- **High Availability**: Introduced Redis-based distributed locking for HA deployments.
- **Database Support**: Added support for PostgreSQL and MySQL/MariaDB.
- **Helm Chart**: Released official Helm chart for Kubernetes deployments.

##### Added Features

**High Availability & Locking**

- Added Redis-based distributed locking for HA deployments.
- Added lock metrics and instrumentation.

**Database**

- Added PostgreSQL and MySQL/MariaDB database support.
- Added database connection pool configuration options.

**Kubernetes & Helm**

- Added official Helm chart with support for Single-Instance and High-Availability modes.
- Added support for existing PVCs and ReadWriteMany access modes in Helm.
- Added security context enhancements, including seccomp profiles.

## [v0.3.0] - 2025-08-28

##### Highlights

- **Observability**: Added Prometheus metrics and OpenTelemetry instrumentation.
- **Performance**: Implemented streaming for NAR downloads.

##### Added Features

**Observability**

- Added Prometheus metrics endpoint.
- Added metrics for NAR file and NAR info file serving,.
- Added OpenTelemetry HTTP instrumentation to upstream cache client.

**Performance**

- Implemented streaming of NAR downloads to clients while still downloading.

**Deployment**

- Added configurable temporary directory for NAR file downloads.
- Added Docker image tags to release notes.

## [v0.2.0] - 2025-04-29

##### Highlights

- **Security**: Enhanced signing capabilities and configuration.

##### Added Features

- Added `/pubkey` route to retrieve the public key.
- Added `--cache-sign-narinfo` flag to disable signing narinfos.

##### Bug Fixes

- Fixed segmentation fault during shutdown.
- Fixed database schema handling to use dbmate's `schema.sql`.

## [v0.1.1] - 2025-01-01

##### Added Features

- Added `--cache-secret-key-path` flag for custom signing keys.

## [v0.1.0] - 2025-01-01

##### Refactoring

- Renamed allow-delete/put flags to `cache-allow-delete/put-verb`.

##### Bug Fixes

- Fixed parsing narinfo with unknown deriver by updating go-nix.

## [v0.0.20] - 2024-12-31

##### Features

- Added timestamp to logger output.
- Added logging flags to nix commands.

##### Bug Fixes

- Downgraded "not found" errors to info level.
- Corrected log messages for nar operations.
