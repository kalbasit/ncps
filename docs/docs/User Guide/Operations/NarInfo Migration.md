# NarInfo Migration

## Overview

NarInfo migration moves NarInfo metadata from storage (filesystem or S3) into the database. This provides faster lookups, better querying capabilities, and prepares for advanced features.

## Why Migrate?

**Benefits:**

- **Faster lookups** - Database queries vs. file I/O
- **Better scalability** - Indexed queries on millions of entries
- **Advanced features** - Enables future features requiring relational data
- **Reduced storage I/O** - Less filesystem/S3 traffic

**When to migrate:**

- Upgrading from pre-database versions
- Moving to high-availability deployments
- Experiencing performance issues with large caches

## Migration Strategies

### Background Automatic Migration (Recommended)

NarInfo metadata is automatically migrated during normal operation when accessed.

**Advantages:**

- Zero downtime
- No manual intervention
- Gradual migration over time
- Works alongside normal cache operation

**How it works:**

1. Client requests a package
1. NCPS checks database first
1. If not in database, reads from storage
1. Migrates to database transparently
1. Subsequent requests use database

**Best for:**

- Production systems
- Caches with moderate traffic
- When downtime is unacceptable

### Explicit CLI Migration

Bulk migration using the CLI command for faster results.

**Advantages:**

- Faster completion
- Predictable timeline
- Progress monitoring
- Deletes from storage after migration

**Disadvantages:**

- Requires downtime (if deleting)
- More manual process

**Best for:**

- Large caches (millions of narinfos)
- Maintenance windows
- When migration speed is important
- Storage space constraints (migration deletes files)

## CLI Migration Guide

### Basic Migration

Migrate all narinfos to database (deletes from storage upon success):

```sh
ncps migrate-narinfo \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

**⚠️ Note:** Migration deletes from storage upon success. Ensure you have backups if needed.

### Distributed Locking with Redis

When migrating while ncps instances are running, use Redis for distributed coordination:

```sh
ncps migrate-narinfo \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-local="/var/lib/ncps" \
  --cache-redis-addrs="redis1.example.com:6379,redis2.example.com:6379,redis3.example.com:6379" \
  --cache-redis-password="your-redis-password" \
  --concurrency=20
```

**With Redis locking:**

- Migration can run safely while ncps is serving requests
- Multiple migration workers coordinate to avoid duplicate work
- Uses distributed locks to prevent race conditions
- Same Redis configuration as your running ncps instances

**Without Redis locking:**

- Uses in-memory locking (no coordination with other instances)
- Should only run when ncps instances are stopped
- Still safe for single-instance deployments

**Redis flags:**

- `--cache-redis-addrs` - Comma-separated Redis server addresses (enables distributed locking)
- `--cache-redis-username` - Redis username (optional)
- `--cache-redis-password` - Redis password (optional)
- `--cache-redis-db` - Redis database number (default: 0)
- `--cache-redis-use-tls` - Use TLS for Redis connections (optional)
- `--cache-redis-pool-size` - Redis connection pool size (default: 10)
- `--cache-lock-backend` - Lock backend to use: 'local', 'redis', or 'postgres' (default: 'local')
- `--cache-lock-redis-key-prefix` - Prefix for Redis lock keys (default: 'ncps:lock:')
- `--cache-lock-allow-degraded-mode` - Fallback to local locks if Redis is down
- `--cache-lock-retry-max-attempts` - Max lock retry attempts (default: 3)

### Dry Run

Preview what would be migrated without making changes:

```sh
ncps migrate-narinfo --dry-run \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

### S3 Storage

For S3-compatible storage:

```sh
ncps migrate-narinfo \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-s3-bucket="ncps-cache" \
  --cache-storage-s3-endpoint="https://s3.amazonaws.com" \
  --cache-storage-s3-region="us-east-1" \
  --cache-storage-s3-access-key-id="..." \
  --cache-storage-s3-secret-access-key="..." \
  --concurrency=50
```

**With Redis for concurrent migration:**

```sh
ncps migrate-narinfo \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-s3-bucket="ncps-cache" \
  --cache-storage-s3-endpoint="https://s3.amazonaws.com" \
  --cache-storage-s3-region="us-east-1" \
  --cache-storage-s3-access-key-id="..." \
  --cache-storage-s3-secret-access-key="..." \
  --cache-redis-addrs="redis1:6379,redis2:6379,redis3:6379" \
  --cache-redis-password="..." \
  --concurrency=50
```

some change to rever

### Concurrency Tuning

Adjust worker count based on your database capacity:

```sh
# Conservative (small database, limited I/O)
--concurrency=5

# Default (balanced)
--concurrency=10

# Aggressive (powerful database, high I/O)
--concurrency=50

# Very aggressive (PostgreSQL with high connection pool)
--concurrency=100
```

**Guidelines:**

- **SQLite**: 5-10 workers (single-writer limitation)
- **PostgreSQL**: 20-100 workers (depends on connection pool)
- **MySQL/MariaDB**: 20-100 workers (depends on connection pool)
- **S3 Storage**: Higher concurrency OK (parallel uploads)

## Progress Monitoring

### Console Output

Migration reports progress every 5 seconds:

```
INFO starting migration
INFO migration progress found=1523 processed=1523 succeeded=1520 failed=3 elapsed=15s rate=101.53
INFO migration progress found=3042 processed=3042 succeeded=3035 failed=7 elapsed=30s rate=101.40
INFO migration completed found=10000 processed=10000 succeeded=9987 failed=13 duration=98.5s rate=101.52
```

**Metrics explained:**

- **found**: Total narinfos discovered
- **processed**: Entered worker pool
- **succeeded**: Successfully migrated
- **failed**: Errors during migration
- **rate**: Narinfos processed per second

### OpenTelemetry

Migration metrics can be exported to an OpenTelemetry collector:

```sh
ncps migrate-narinfo \
  --otel-enabled \
  --otel-grpc-url="http://otel-collector:4317" \
  ...
```

If OpenTelemetry is enabled, monitor via metrics:

**ncps_migration_narinfos_total**

```
# Total migrations
sum(ncps_migration_narinfos_total)

# Success rate
sum(rate(ncps_migration_narinfos_total{result="success"}[5m])) /
sum(rate(ncps_migration_narinfos_total[5m]))
```

**ncps_migration_duration_seconds**

```
# Average migration time
histogram_quantile(0.5, ncps_migration_duration_seconds)

# 99th percentile
histogram_quantile(0.99, ncps_migration_duration_seconds)
```

**ncps_migration_batch_size**

```
# Batch sizes
histogram_quantile(0.5, ncps_migration_batch_size)
```

## Verification

### Check Migration Status

**Query migrated count:**

```sh
# SQLite
sqlite3 /var/lib/ncps/db.sqlite "SELECT COUNT(*) FROM narinfos WHERE url IS NOT NULL;"

# PostgreSQL
psql -h localhost -U ncps -d ncps -c "SELECT COUNT(*) FROM narinfos WHERE url IS NOT NULL;"

# MySQL
mysql -u ncps -p ncps -e "SELECT COUNT(*) FROM narinfos WHERE url IS NOT NULL;"
```

**Query unmigrated count:**

```
SELECT COUNT(*) FROM narinfos WHERE url IS NULL;
```

### Spot Check

Verify specific narinfos migrated correctly:

```
SELECT hash, store_path, url, compression, nar_size
FROM narinfos
WHERE hash = 'n5glp21rsz314qssw9fbvfswgy3kc68f';
```

## Troubleshooting

### Migration is Slow

**Symptoms:** Low processing rate, taking too long

**Solutions:**

1. **Increase worker count** (if database can handle it)

   ```sh
   --concurrency=50
   ```

1. **Check database connection pool**

   ```sh
   --cache-database-pool-max-open-conns=100
   ```

1. **Verify network latency** to database

1. **Run during low-traffic period**

1. **For SQLite**: Consider PostgreSQL/MySQL for better concurrency

### Duplicate Key Errors in Logs

**Symptoms:** Logs show "duplicate key" errors

**Explanation:** Normal during concurrent operations. Multiple workers may try to create the same record.

**Solution:** System handles gracefully - no action needed. These are logged for observability but don't affect migration.

### Storage Deletions Failed

**Symptoms:** Migration partially succeeded but some storage deletions failed

**Solution:** Re-run the migration to retry deletions:

```sh
ncps migrate-narinfo \
  --cache-database-url="..." \
  --cache-storage-local="..."
```

**How it works:**

- Migration is idempotent
- Already-migrated narinfos are deleted from storage
- Database migration step is skipped

### Transaction Deadlocks

**Symptoms:** Database deadlock errors in logs

**Solutions:**

1. **Reduce worker count**

   ```sh
   --concurrency=5
   ```

1. **Use PostgreSQL/MySQL** instead of SQLite (better concurrent writes)

### Out of Memory

**Symptoms:** Process killed or OOM errors

**Solutions:**

1. **Migration loads all migrated hashes** into memory by default

   - For very large caches (millions of narinfos), this can use significant RAM
   - Solution: Ensure adequate memory or use background migration instead

1. **Reduce worker count** to lower memory pressure

   ```sh
   --concurrency=10
   ```

## Best Practices

### Before Migration

1. **Backup database** before starting

   ```sh
   # SQLite
   cp /var/lib/ncps/db.sqlite /var/lib/ncps/db.sqlite.backup

   # PostgreSQL
   pg_dump ncps > ncps_backup.sql
   ```

1. **Test with dry run**

   ```
   ncps migrate-narinfo --dry-run ...
   ```

1. **Check available disk space** as the database will grow.

1. **Plan for a maintenance window** since this is a destructive operation.

### During Migration

1. **Monitor progress** via console or OpenTelemetry
1. **Watch error count** - some failures OK, many failures = investigate
1. **Check database performance** - watch for resource constraints
1. **Keep backups available** for quick rollback if needed

### After Migration

1. **Verify migration count** matches expected
1. **Spot check** several narinfos for data integrity
1. **Test cache operation** - fetch a few packages
1. **Keep storage files** for a few days before deleting (safety)
1. **Monitor cache performance** - should improve after migration

## Common Workflows

### Incremental Migration

Migrate in batches during low-traffic periods:

```sh
# Week 1: Dry run to estimate
ncps migrate-narinfo --dry-run ...

# Week 2: Migrate (keep in storage)
ncps migrate-narinfo ...

# Week 3: Verify and test
# ... verify in database, test cache operation ...

# Week 4: Run migration to delete from storage
ncps migrate-narinfo ...
```

### High-Availability Migration

For multi-instance deployments with Redis:

**Option 1: Zero-downtime migration (with Redis locking)**

```sh
# Migration can run while instances are serving requests
# Use the SAME Redis configuration as your running instances
ncps migrate-narinfo \
  --cache-database-url="postgresql://..." \
  --cache-storage-s3-bucket="..." \
  --cache-redis-addrs="redis1:6379,redis2:6379,redis3:6379" \
  --cache-redis-password="..." \
  --concurrency=50
```

**Benefits:**

- No downtime required
- Migration coordinates with running instances via distributed locks
- Safe to run multiple migration processes simultaneously
- Each narinfo is migrated exactly once (lock prevents duplicates)

**Option 2: Maintenance window (without Redis)**

```sh
# 1. Stop all ncps instances
systemctl stop ncps@*

# 2. Run migration (no Redis needed)
ncps migrate-narinfo \
  --cache-database-url="postgresql://..." \
  --cache-storage-s3-bucket="..." \
  --concurrency=50

# 3. Start all instances
systemctl start ncps@*
```

**When to use each approach:**

- **With Redis**: Production systems where downtime is unacceptable, or when you want to parallelize migration across multiple machines
- **Without Redis**: Maintenance windows, single-instance deployments, or when Redis is not available

### Emergency Rollback

If migration causes issues:

```sh
# 1. Stop service
systemctl stop ncps

# 2. Restore database backup
cp /var/lib/ncps/db.sqlite.backup /var/lib/ncps/db.sqlite

# 3. Start service (will use storage files)
systemctl start ncps
```

Storage files are still available (unless you used `--delete`).

## Next Steps

- <a class="reference-link" href="Monitoring.md">Monitoring</a> - Track migration metrics
- <a class="reference-link" href="Upgrading.md">Upgrading</a> - Upgrade procedures
- <a class="reference-link" href="../Configuration/Database.md">Database</a> - Database configuration

## Related Documentation

- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Storage backends
- <a class="reference-link" href="../Configuration/Database.md">Database</a> - Database setup
- <a class="reference-link" href="Troubleshooting.md">Troubleshooting</a> - Common issues
