# NAR to Chunks Migration

## Overview

NAR to Chunks migration moves NAR files from traditional storage (filesystem or S3) into Content-Defined Chunks (CDC). This process deduplicates data across NAR files, significantly reducing storage usage.

> [!CAUTION]
> **EXPERIMENTAL FEATURE** Content-Defined Chunking is currently an experimental feature and may undergo significant changes in future releases. Use with caution in production environments.

## Why Migrate?

**Benefits:**

- **Storage Efficiency** - Drastic reduction in storage usage by deduplicating common data across all NAR files.
- **Improved Performance** - Future features like faster synchronization and differential updates rely on chunked storage.
- **De-duplication** - Multiple versions of the same package or packages sharing dependencies will share the same chunks.

**When to migrate:**

- You have enabled CDC and want to deduplicate existing NAR files.
- You are running low on storage space and want to reclaim it.
- You want to transition fully to Content-Defined Chunking.

## CLI Migration Guide

### Basic Migration

Migrate all NAR files to chunks. Once a NAR is successfully migrated and verified, it is deleted from the original storage:

```sh
ncps migrate-nar-to-chunks \
  --cache-hostname="cache.example.com" \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

**⚠️ Note:** Migration deletes from the original storage upon success. Ensure you have backups if needed.

### Dry Run

Preview what would be migrated without making changes:

```sh
ncps migrate-nar-to-chunks --dry-run \
  --cache-hostname="cache.example.com" \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

### Concurrency Tuning

Adjust worker count based on your system's capacity:

```sh
# Conservative
--concurrency=5

# Default
--concurrency=10

# Aggressive
--concurrency=50
```

### S3 Storage

For S3-compatible storage:

```sh
ncps migrate-nar-to-chunks \
  --cache-hostname="cache.example.com" \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-s3-bucket="ncps-cache" \
  --cache-storage-s3-endpoint="https://s3.amazonaws.com" \
  --cache-storage-s3-region="us-east-1" \
  --cache-storage-s3-access-key-id="..." \
  --cache-storage-s3-secret-access-key="..." \
  --concurrency=20
```

## Progress Monitoring

The migration command reports progress every 5 seconds:

```
INFO starting migration of NARs to chunks
INFO migration progress found=1523 processed=1523 succeeded=1520 failed=3 skipped=0 elapsed=15s rate=101.53
INFO migration progress found=3042 processed=3042 succeeded=3035 failed=7 skipped=120 elapsed=30s rate=101.40
INFO migration completed found=10000 processed=10000 succeeded=9880 failed=13 duration=98.5s
```

**Metrics explained:**

- **found**: Total NARs discovered in the store.
- **processed**: NARs that have been picked up by workers.
- **succeeded**: NARs successfully chunked and verified.
- **failed**: Errors during chunking or verification.
- **skipped**: NARs already present in the chunk store.
- **rate**: NARs processed per second.

## Monitoring and Metrics

When OpenTelemetry is enabled (`--otel-enabled`), the migration process exports metrics that can be used for monitoring and dashboarding.

### Available Metrics

- `ncps_migration_narinfos_total{migration_type="nar-to-chunks",operation,result}` - Total NARs processed.
- `ncps_migration_duration_seconds{migration_type="nar-to-chunks",operation}` - Duration of chunking operations.
- `ncps_migration_batch_size{migration_type="nar-to-chunks"}` - Total number of NARs found for migration.

### Example PromQL Queries

**Migration throughput:**

```
rate(ncps_migration_narinfos_total{migration_type="nar-to-chunks"}[5m])
```

**Migration success rate:**

```
sum(rate(ncps_migration_narinfos_total{migration_type="nar-to-chunks",result="success"}[5m]))
/ sum(rate(ncps_migration_narinfos_total{migration_type="nar-to-chunks"}[5m]))
```

**Migration duration (p99):**

```
histogram_quantile(0.99, ncps_migration_duration_seconds{migration_type="nar-to-chunks"})
```

## Verification

### Check Storage Usage

You should see a decrease in total storage usage (sum of `nar/` and `chunks/` directories) after migration, as original NAR files are deleted.

### Database Inspection

You can check the `nar_chunks` table to see the mapping:

```mariadb
SELECT count(DISTINCT nar_id) FROM nar_chunks;
```

## Troubleshooting

### Migration is Slow

- **Increase concurrency**: If your CPU and disk I/O allow it.
- **Check Database Pool**: For PostgreSQL/MySQL, ensure the connection pool is large enough.
- **Cache Temp Path**: Use a fast disk for `--cache-temp-path` (default is system temp).

### "Already in Chunks" Skipping

If the logs show many skipped NARs, it means they were already processed. If you believe this is in error, check if the chunks actually exist in the storage backend under the `chunks/` prefix.

### Failed Deletions

If chunking succeeds but deleting the original NAR fails, it will be logged as a warning. The NAR will remain in the original storage and will be retried if the migration is run again.

## Related Documentation

- [CDC Feature Overview](../Features/CDC.md)
- [Storage Configuration](../Configuration/Storage.md)
- [Database Configuration](../Configuration/Database.md)
