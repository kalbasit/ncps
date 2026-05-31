# Chunks to NAR Migration

## Overview

Chunks to NAR migration is the reverse of [NAR to Chunks Migration](NAR%20to%20Chunks%20Migration.md): it reconstructs Content-Defined-Chunked (CDC) NARs back into whole files. This lets a deployment **exit CDC** — for example after deciding CDC is counter-productive for the storage backend in use (see the backend guidance under [Storage Configuration](../Configuration/Storage.md)).

> [!CAUTION]
> **EXPERIMENTAL FEATURE** Content-Defined Chunking is currently experimental and may change in future releases. Use with caution in production environments.

## Why Migrate Back?

CDC's many small chunk reads suit low-latency storage (local SSD, NVMe-backed object stores). On high-latency or spinning-disk backends — especially a network filesystem — whole-file serving is more robust. Because `migrate-nar-to-chunks` deletes the original whole files (after `cdc.delete-delay`), there is otherwise no supported way back to whole-file storage; this command provides it.

**When to migrate back:**

- You enabled CDC and found it counter-productive for your storage backend.
- You are decommissioning CDC and want every NAR served as a whole file before disabling it.

## How It Works

For each chunked NAR, `ncps`:

1. **Reconstructs** the whole NAR by concatenating its chunks in order.
1. **Verifies** the reconstructed bytes against the linked narinfo's recorded `NarHash` (and size). A NAR is never de-chunked unless it verifies — _verified-or-nothing_.
1. **Writes** the whole file to the NAR store.
1. **Flips** the `nar_file` record to the whole-file representation (drops chunk links, `total_chunks = 0`).
1. **Leaves** the now-unreferenced chunks for the garbage collector (see [Chunk reclamation](#chunk-reclamation)).

NARs whose narinfo has **no recorded `NarHash`** cannot be content-verified and are **skipped** (left chunked), not de-chunked unverified.

## CLI Migration Guide

### Basic Migration

```sh
ncps migrate-chunks-to-nar \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

This reconstructs and verifies every chunked NAR, writes the whole file, and flips the record. Now-orphaned chunks are left for the GC by default (see below).

### Dry Run

Preview what would be migrated without making changes:

```sh
ncps migrate-chunks-to-nar --dry-run \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

### Chunk reclamation

By **default**, the migration does **not** delete the now-orphaned chunk files. A client whose download began streaming from chunks before the record was flipped may still be reading those chunk files, and deleting them mid-stream would truncate that transfer. The unreferenced chunks are left for the regular garbage collector to reclaim.

To reclaim the freed space **immediately**, pass `--force-reclaim` — but only when traffic is drained (e.g. a maintenance window):

```sh
ncps migrate-chunks-to-nar --force-reclaim \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

Reclamation is always **dedup-safe**: a chunk still referenced by another (still-chunked) NAR is never deleted, with or without `--force-reclaim`.

> [!NOTE]
> `--force-reclaim` only reclaims chunks orphaned by NARs migrated in _that_ run. If you ran the default (deferred) migration first, run the garbage collector (or run with `--force-reclaim` is a no-op for already-flipped NARs) — those chunks are reclaimed on the next GC cycle. Reclaim **before** disabling CDC, since a disabled-CDC GC may not sweep chunks.

### Concurrency Tuning

```sh
--concurrency=5    # Conservative
--concurrency=10   # Default
--concurrency=50   # Aggressive
```

### S3 Storage

```sh
ncps migrate-chunks-to-nar \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-s3-bucket="ncps-cache" \
  --cache-storage-s3-endpoint="https://s3.amazonaws.com" \
  --cache-storage-s3-region="us-east-1" \
  --cache-storage-s3-access-key-id="..." \
  --cache-storage-s3-secret-access-key="..." \
  --concurrency=20
```

### Running via the Helm chart

The chart ships an opt-in, one-off Job (a `post-install`/`post-upgrade` hook) that runs `migrate-chunks-to-nar` with the same flags. Enable it for the upgrade that performs the migration, then disable it again:

```yaml
migrateChunksToNar:
  enabled: true
  dryRun: false
  # Only with traffic drained — the Job runs post-upgrade while pods may still serve.
  forceReclaim: false
  concurrency: 10
```

`forceReclaim` maps to `--force-reclaim` and `dryRun` to `--dry-run`. The Job reuses the deployment's database, storage, and Redis configuration. Because it is gated by `migrateChunksToNar.enabled`, nothing is rendered when disabled (the default).

## Exiting CDC

After running `migrate-chunks-to-nar` to completion, disable CDC to stop storing new NARs as chunks:

**1. Confirm no chunked NARs remain** (optional but recommended):

```sql
-- Should return 0 after a complete migration:
SELECT count(*) FROM nar_files WHERE total_chunks > 0;
```

**2. Set `cdc.enabled: false`** in your config or Helm values and restart:

```yaml
# values.yaml (Helm) — set both in the same upgrade
config:
  cdc:
    enabled: false
migrateChunksToNar:
  enabled: false   # turn off the migration job after it has run
```

Or in the ncps config file:

```yaml
cache:
  cdc:
    enabled: false
```

**3. Restart the server.** On startup, ncps:

- Clears the stored CDC configuration from the database.
- Logs a warning if any chunked NARs remain, including the count, so you can decide whether to re-run the migration before traffic ramps up.
- Starts normally regardless — any remaining chunked NARs are treated as cache misses and re-fetched from upstream on demand.

> [!NOTE]
> Skipped NARs ("no narinfo NarHash to verify against") are left chunked. If you disable CDC with these remaining, they will be re-fetched from upstream on the next request for each one. This is safe but may result in upstream traffic for those NARs.

**Re-enabling CDC** after disabling it is treated as a fresh first boot — you can change chunk sizes freely.

## Exit Codes & Failure Isolation

A per-NAR failure (hash mismatch, missing chunk, I/O error) is recorded and does **not** abort the batch — every other NAR is still processed. The command **exits non-zero** if any NAR failed, reporting the count. A NAR that fails verification keeps its chunks and record intact.

## Monitoring and Metrics

When OpenTelemetry is enabled (`--otel-enabled`), the process exports:

- `ncps_migration_objects_total{migration_type="chunks-to-nar",operation,result}` - Total NARs processed.
- `ncps_migration_duration_seconds{migration_type="chunks-to-nar",operation}` - Duration histogram.
- `ncps_migration_batch_size{migration_type="chunks-to-nar"}` - Total chunked NARs found.

## Verification

```mariadb
-- No chunked NARs should remain after a full migration:
SELECT count(*) FROM nar_files WHERE total_chunks > 0;
```

## Troubleshooting

### NARs are skipped with "no narinfo NarHash to verify against"

Those narinfos predate `NarHash` recording, so the reconstructed bytes cannot be verified and the NAR is intentionally left chunked. Re-fetch/refresh the narinfo (so it gains a `NarHash`), then re-run.

### Space was not reclaimed

By default chunks are left for the GC. Run with `--force-reclaim` during a maintenance window, or let the GC run, **before** disabling CDC.

## Related Documentation

- [NAR to Chunks Migration](NAR%20to%20Chunks%20Migration.md) - The forward migration
- [CDC Feature Overview](../Features/CDC.md)
- [Storage Configuration](../Configuration/Storage.md)
