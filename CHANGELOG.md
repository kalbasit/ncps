# Changelog

All notable changes to ncps are recorded in this file. The format roughly
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project loosely follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **In-flight NAR staging for cross-pod serving during download.** A replica
  holding a download can now serve the in-flight NAR to other replicas while it
  is still downloading, by staging it to shared storage as ordered, immutable,
  fixed-size part-objects once a second replica waits for the same NAR. Waiters
  tail the parts to reassemble a complete, byte-correct NAR — closing the
  cross-pod serve-during-download gap for **all** modes (non-CDC, lazy-CDC,
  eager-CDC) on S3, and superseding the fragile progressive-chunk fallback during
  the eager-CDC chunking window. It is gated by `--cache-inflight-staging-enabled`
  (env `CACHE_INFLIGHT_STAGING_ENABLED`, **off by default**), tuned by
  `--cache-inflight-staging-retention` (GC grace, default `5m`) and
  `--cache-inflight-staging-part-size` (default 8 MiB), and exposed in the Helm
  chart as `config.inflightStaging.*`. It only activates with a distributed
  (Redis) lock and has **zero overhead until a second replica contends** for the
  same NAR. This makes HA viable without CDC. (#660, #1289)

- **`build-trace-v2` endpoint.** `nix copy` unconditionally PUTs and GETs
  build-trace entries at `/build-trace-v2/{drvName}/{outputName}.doi` for
  content-addressed derivations. Previously ncps had no routes for these paths
  and returned 404, causing `nix copy` to exit non-zero even when the NAR
  upload succeeded. (#1272)

- **CDC drain mode.** Disabling CDC (`--cache-cdc-enabled=false`) on a
  deployment that still has chunked NARs in the database no longer makes those
  NARs cache misses. On startup ncps detects the mismatch, initializes the chunk
  store read-only, and continues serving chunked NARs while writes go to whole
  files. Once all chunks have been migrated away the drain completes
  automatically — the next restart starts fully CDC-disabled with no operator
  action required. (#1305)

- **`ncps migrate-chunks-to-nar` command.** Reverse of `migrate-nar-to-chunks`:
  reconstitutes whole NAR files from their stored chunks, updates the database,
  and reclaims the chunk objects. Supports `--dry-run`, `--concurrency`, and
  `--force-reclaim` (bypasses the in-flight-serve safety check). Per-NAR
  failures are isolated — the batch continues and the command exits non-zero
  only if any NAR failed. (#1301)

- **`--cache-cdc-chunk-wait-timeout` flag** (env: `CACHE_CDC_CHUNK_WAIT_TIMEOUT`,
  default `30s`). Controls how long progressive CDC streaming waits for each
  chunk before giving up. Previously hard-coded, the new flag lets operators
  align the timeout with their reverse-proxy timeout to avoid spurious 504s on
  high-latency storage. Exposed as `cache.cdc.chunkWaitTimeout` in the Helm
  chart. (#1299, #1300)

- **Helm: `migrate-nar-to-chunks` Job.** The forward CDC migration Job (whole
  NAR → chunks) now has a chart representation. Both `migrate-nar-to-chunks` and
  `migrate-chunks-to-nar` Jobs are disabled by default (`enabled: false`) and
  auto-cleanup via `ttlSecondsAfterFinished: 3600`. (#1306)

- **`ncps fsck` command.** A new top-level command that walks the database and
  storage backend to detect and repair inconsistencies (orphaned chunks,
  dangling narinfos, NAR rows with missing bytes, and stale state). Supports
  progress reporting, a `verified_at` watermark to skip recently-checked rows
  via `--verified-at`, and deep `--verify-content` verification that re-hashes
  stored NARs. Exposed in the Helm chart as a periodic **fsck CronJob**
  (`fsck.enabled`, disabled by default) with a configurable schedule,
  resources, security context, and concurrency policy. (#975, #996, #998,
  #999, #1000, #1004, #1006, #1176)

- **Closure pinning.** Narinfos (and their full closure) can now be pinned to
  protect them from LRU eviction. New HTTP endpoints: `POST /pin/{hash}.narinfo`
  to pin, `DELETE /pin/{hash}.narinfo` to unpin, and `GET /pins` to list pinned
  closures. Pinned closures are skipped by the LRU cleanup. (#1108)

- **pprof profiling server.** A new optional pprof endpoint for live profiling,
  enabled via `--pprof-addr` (env `PPROF_ADDR`, e.g. `:6060`); empty (the
  default) leaves it disabled. (#1063)

- **CDC lazy chunking.** NARs can be served and stored as whole compressed
  files immediately and chunked lazily in the background, with delayed deletion
  of the compressed copy, reducing time-to-first-byte. Gated by
  `--cache-cdc-lazy-chunking-enabled` and tuned by
  `--cache-cdc-lazy-cleanup-schedule`, with a background recovery cron that
  re-drives interrupted lazy chunking. Exposed in the Helm chart under
  `cache.cdc.*`. (See also the default-changed note below.) (#1082, #1083,
  #1089, #1096)

- **Helm: automatic `GOMEMLIMIT` from the memory limit.** When
  `resources.limits.memory` is set and `GOMEMLIMIT` is not already defined in
  the pod env, the chart now derives `GOMEMLIMIT` from the container memory
  limit so the Go runtime respects the cgroup ceiling. (#1061)

- **Helm: configurable temporary-directory volume.** The download/scratch
  directory (`tempPath`, default `/tmp/ncps`) now has a configurable
  `tempVolume`, including support for a `volumeClaimTemplate` in the
  StatefulSet deployment mode. (#1074, #1077)

- **Helm: `Recreate` deployment strategy for SQLite.** Deployments backed by
  SQLite are now forced to `strategy: type: Recreate` to avoid two pods sharing
  a single-writer database file across a rolling update. (#1012)

- **Startup storage reporting and migration warning.** ncps now reports the
  active storage type and mode at startup, and warns when narinfo storage is in
  the deprecated layout and must be migrated. (#959, #961)

- **`docker-dev` image.** A new development image plus an `update-cu-base`
  script supporting container-use workflows. (#1097)

### Fixed

- **Compressed upstream narinfos that omit `FileSize`/`FileHash` are no longer
  rejected.** Some upstreams (e.g. niks3, nix-serve-style servers) emit narinfos
  declaring a compression algorithm but without the optional `FileSize`/`FileHash`
  fields, which ncps treated as fatal — returning a 404 for every request and
  making the upstream unusable. ncps now accepts these narinfos and, for compressed
  NARs served whole, computes the correct `FileSize` and `FileHash` itself from the
  stored compressed bytes (a single streaming SHA-256 pass) and backfills them into
  the persisted narinfo record, which subsequent narinfo responses then reflect.
  Upstream-provided values are preserved unchanged. (#1314)

- **Helm: migration Job no longer mounts storage or tmp volumes for non-SQLite
  databases.** The migration Job was unconditionally mounting the storage PVC and
  an 8 GiB in-memory `tmp` emptyDir even for PostgreSQL/MySQL deployments.
  `ncps migrate up` only opens a database connection and never touches the
  filesystem, so both mounts were unnecessary and wasted resources. (#1267)

- **Helm: migration Jobs are no longer ArgoCD sync hooks.** The
  `helm.sh/hook` annotations caused OOMKilled jobs to block all subsequent ArgoCD
  syncs until manually deleted. Both migration Jobs are now regular release
  resources. (#1306)

- **Cache reliability on shared/high-latency storage (NFS, multi-replica).** A
  cluster of related fixes for deployments using the `local` backend on a network
  filesystem:

  - `HasNar` previously collapsed every storage error into `false`, making an
    ambiguous NFS stat indistinguishable from a confirmed absence and triggering
    a destructive narinfo purge. `HasNar` now distinguishes *present* /
    *confirmed-absent* / *unknown*; unknown results skip the purge and let the
    next request re-evaluate. (#1299)
  - Hardened NAR cache-miss recovery: transient errors are retried, genuine 404s
    are not, and the recovery sweep correctly re-drives rows that have a backing
    file but a stale database state. (#1296)
  - CDC recovery follow-ups: improved GC, exponential backoff, and streaming
    robustness. (#1297)
  - Download-lock contention no longer returns HTTP 500 to the client. (#1290)
  - Fixed a hang when a client cancels a request mid-stream during a concurrent
    NAR download. (#1280)
  - Fixed spurious narinfo purge triggered by a concurrent fetch that raced the
    narinfo insert. (#1279)

- **CDC: fixed compression mismatch in lazy-chunking.** CDC lazy-chunking was
  writing chunks with the wrong compressor in some cases, producing corrupt
  reassembly on read. (#1255)

- **PostgreSQL: fsck no longer hits the 65,535-parameter IN-clause cap.** Large
  databases caused fsck queries to exceed PostgreSQL's bind-parameter limit.
  Queries are now batched. (#1268)

- **PostgreSQL: identity sequence reliably synced on existing databases.** The
  `postgres_serial_to_identity` migration could leave sequences at their initial
  low value on databases that already had rows, causing duplicate-key errors on
  insert. The migration now uses `ALTER TABLE … ALTER COLUMN id RESTART WITH (MAX(id)+1)` inside a DO block, which targets the identity sequence directly
  regardless of its internal name. (#1258)

- **PostgreSQL: concurrent narinfo inserts no longer produce 25P02 errors.**
  Chunk inserts now use `DO NOTHING` with 25P02 (in-failed-transaction) recovery,
  and concurrent narinfo upserts are serialized correctly. (#1259, #1262)

- **Cache: stub narinfo filled correctly under concurrent race.** A race between
  two goroutines resolving the same stub narinfo could leave one with an empty
  result. (#1263)

- **Storage: `store/tmp` is no longer wiped on startup.** The `local` backend
  previously cleared its store/tmp scratch directory on boot, which could
  discard in-progress or shared data on a restart. (#1313)

- **NAR hash verification uses `nix-hash` for nix32 conversion.** NAR content
  hashes are now verified using `nix-hash`-compatible nix32 encoding, fixing
  spurious verification mismatches. (#1101)

- **Prometheus metrics are exported from startup.** OTEL counters
  (`ncps_nar_served_total`, etc.) were not exported until their first
  increment, so freshly-started or idle instances exposed nothing despite the
  documented metrics. Counters are now primed at startup. (#1337, #1343)

- **Un-reassemblable chunked NARs return 404, not a truncated 200.** A chunked
  NAR that cannot be fully reassembled now fails cleanly with 404 instead of
  streaming a truncated body with a 200 status. (#1319)

- **A purged narinfo is never surfaced as HTTP 500.** Concurrent purge of a
  narinfo during a read no longer returns a 500 to the client. (#1318)

- **Fail-fast boot validation for the storage tmp directory.** ncps now
  validates that its temporary storage directory is writable at startup and
  fails fast with a clear error instead of failing later mid-request. (#1086)

- **Additional CDC/cache reliability fixes.**

  - Heal orphaned mid-chunking NAR state via migration-lock liveness. (#1317)
  - Serve a whole-file NAR with its true compression under CDC instead of
    relabeling it. (#1346)
  - Delete the stored variant (not just the DB row) on NAR eviction. (#1341)
  - Match unlinked prefixed narinfo URLs hash-aware. (#1342)
  - Tolerate opaque (non-hash) upstream NAR URLs. (#1331)
  - Docker: ship `/etc/passwd` and `/etc/group` as regular files so the image
    runs under restrictive runtimes that reject symlinked identity files.
    (#1347)

### Changed

- **CDC lazy chunking is now opt-in (default: `false`).** In v0.9, lazy
  chunking was enabled by default after being introduced in #1081. Enabling it
  silently on upgrade starts background workers, a cleanup cron job, and delays
  compressed NAR deletion without operator consent. The default has been
  reverted to `false`; set `--cache-cdc-lazy-chunking-enabled=true` (or
  `cache.cdc.lazyChunkingEnabled: true` in the Helm chart) to restore the
  previous behavior. (#1172)

- **CDC configuration is now persisted in the database config table.** The
  active CDC settings are stored in the `config` table so they survive restarts
  and stay consistent across replicas, rather than being derived from flags
  alone on every boot. (#971)

- **Helm chart: security context defaults removed; `containerDefaults.securityContext` added.**
  All default values have been removed from `podSecurityContext`, `securityContext`,
  and all per-container securityContext blocks (`migration`, `fsck`,
  `migrateChunksToNar`, `migrateNarToChunks`). A new `containerDefaults.securityContext`
  key provides a global fallback applied to every container via deep-merge
  (per-container values win). A new `initImage.securityContext` key controls the
  `create-db-dir` busybox init container, which previously hardcoded
  `runAsUser/runAsGroup: 1000` and overrode pod-level identity.

  **Breaking change for bare installations.** Containers will run without any
  hardening constraints unless the operator explicitly sets values. To restore
  the previous posture, add to your `values.yaml`:

  ```yaml
  podSecurityContext:
    runAsNonRoot: true
    runAsUser: 1000
    runAsGroup: 1000
    fsGroup: 1000
    fsGroupChangePolicy: OnRootMismatch
    seccompProfile:
      type: RuntimeDefault

  containerDefaults:
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop: [ALL]
      readOnlyRootFilesystem: true
      runAsNonRoot: true
      runAsUser: 1000
      runAsGroup: 1000
  ```

- **Database tooling migrated from sqlc + dbmate to Ent + Atlas + Goose.**
  Schemas are now authored under `ent/schema/*.go`, migrations are
  generated from Atlas diffs (used as a Go library) via
  `task migrations:gen NAME=<descriptive_snake_case>` (which regenerates
  the Ent client via its dependency on `ent:generate`, then calls
  `go run ./cmd/generate-migrations --name=...`), and applied at runtime
  by `ncps migrate up`. The runtime applier is `goose.NewProvider`
  against the embedded `migrations/<dialect>/` FS.

  See `CLAUDE.md` for the full developer workflow and the
  expand-contract policy + four-step NOT NULL recipe.

### Removed

- **BREAKING (Helm): `config.cdc.iLoveTimeouts` removed.** The HA-validation
  bypass flag no longer exists. The `replicaCount > 1` guard is now satisfied by
  enabling **either** CDC (`config.cdc.enabled=true`) **or** in-flight NAR staging
  (`config.inflightStaging.enabled=true`). Because in-flight staging is
  zero-overhead-until-contention, every HA operator can satisfy the guard safely,
  so the accept-the-risk escape hatch protected no remaining use case. **Migration:**
  replace `config.cdc.iLoveTimeouts: true` with `config.inflightStaging.enabled: true`
  (or `config.cdc.enabled: true`). Supplying the now-unrecognized `iLoveTimeouts`
  has no effect and will not bypass the guard. (#660)

- The `dbmate` and `dbmate-wrapper` binaries are no longer shipped in
  the dev shell or in Docker images.

- The `sqlc` codegen tooling and the generated `pkg/database/*db/`
  wrapper packages have been removed; callers now use the Ent client
  directly via `*database.Client`.

### Migration (operators)

**If you are upgrading an existing dbmate-managed deployment, BACK UP
YOUR DATABASE first.** The migration is forward-only and rollback
requires a restore.

The first `ncps migrate up` after upgrading performs a one-shot
adoption:

1. The new migrator inspects the existing `schema_migrations` table.
1. If the shape is the legacy dbmate one, it converts the tracking
   table to the goose shape:
   - On SQLite and PostgreSQL — inside a single transaction
     (`BEGIN; CREATE TEMP …; DROP TABLE schema_migrations; CREATE TABLE schema_migrations (goose shape); INSERT sentinel + preserved versions; verify row-count consistency (including the sentinel row); COMMIT;`).
   - On MySQL — via a RENAME → CREATE → sentinel → copy → verify →
     DROP backup-table dance that is safe to interrupt and resume.
1. All previously applied dbmate versions are recorded as
   goose-applied, so the new migrator picks up only the truly pending
   migrations.
1. The normal goose apply path then runs.

Adoption is idempotent — re-running after success is a no-op.

Operators with very large databases should run
`ncps migrate up --dry-run` first to preview the detected state and
adoption action without touching the database.

### CI

- New `nix flake check` derivations:
  - `ent-codegen-drift-check` — regenerates `ent/` and fails on diff.
  - `ent-lint-check` — runs `cmd/ent-lint --root .`; fails on any
    `[FAIL]` line.
  - `atlas-sum-check` — verifies every `migrations/<dialect>/atlas.sum`
    matches the directory contents.
  - `schema-equivalence-check` — runs the `TestSchemaEquivalence`
    golden test across SQLite, PostgreSQL, and MySQL.
