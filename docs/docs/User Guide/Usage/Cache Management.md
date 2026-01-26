# Cache Management

## Cache Management Guide

Manage cache size, cleanup, and optimization.

## Size Limits

### Configure Maximum Size

**Command-line:**

```
ncps serve --cache-max-size=100G
```

**Configuration file:**

```yaml
cache:
  max-size: 100G
```

**Size formats:**

- `10K` - 10 kilobytes
- `100M` - 100 megabytes
- `50G` - 50 gigabytes
- `1T` - 1 terabyte

### Check Current Size

**Local storage:**

```
du -sh /var/lib/ncps
```

**S3 storage:**

```
aws s3 ls --summarize --recursive s3://ncps-cache/
```

## LRU Cleanup

### Automatic Cleanup

Configure automatic LRU (Least Recently Used) cleanup:

**Configuration:**

```yaml
cache:
  max-size: 100G
  lru:
    schedule: "0 2 * * *"  # Daily at 2 AM (cron format)
```

**Cron schedule examples:**

- `0 2 * * *` - Daily at 2 AM
- `0 */6 * * *` - Every 6 hours
- `0 3 * * 0` - Weekly on Sunday at 3 AM

### Manual Cleanup

Trigger cleanup manually (not implemented via API, use systemctl/docker restart with updated config).

## Monitoring

### Cache Statistics

**Check logs** for cache operations:

```
# Docker
docker logs ncps | grep "cache"

# Systemd
journalctl -u ncps | grep "cache"
```

### Prometheus Metrics

If Prometheus is enabled:

**Cache size:**

- Custom script to export size metrics

**Cache hits/misses:**

- `ncps_nar_served_total` - Total NARs served
- `ncps_narinfo_served_total` - Total NarInfo served

**Query cache hit rate:**

```
rate(ncps_nar_served_total[5m])
```

See <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> for dashboards.

## Storage Optimization

### Identify Large Packages

**Local storage:**

```
find /var/lib/ncps/nar -type f -size +100M | sort -h
```

**S3 storage:**

```
aws s3 ls --recursive s3://ncps-cache/nar/ | sort -k3 -n | tail -20
```

### Delete Specific Packages

**Warning:** Manual deletion not recommended. Use LRU cleanup instead.

If absolutely necessary:

```
# For local storage
rm /var/lib/ncps/nar/<hash>.nar

# Also remove from database
# (requires database access and SQL knowledge)
```

## NarInfo Migration

### What is NarInfo Migration?

NarInfo migration moves NarInfo metadata from storage (files) into the database for faster lookups and better scalability.

### Background Migration (Automatic)

NarInfo metadata is automatically migrated during normal operation when accessed. No action required.

**How it works:**

- Client requests a package
- NCPS checks database first
- If not found, reads from storage and migrates
- Subsequent requests use faster database lookups

### Explicit Migration (CLI)

For faster bulk migration and to free up storage space:

**Basic migration (deletes after migration):**

```sh
ncps migrate-narinfo \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps" \
  --concurrency=20
```

**With Redis (for concurrent migration while serving):**

```sh
ncps migrate-narinfo \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps" \
  --cache-redis-addrs="redis1:6379,redis2:6379,redis3:6379" \
  --cache-redis-password="..." \
  --concurrency=20
```

**Dry run (preview):**

```sh
ncps migrate-narinfo --dry-run \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

### Migration Progress

Monitor migration progress through:

- **Console logs** - Progress updates every 5 seconds
- **Prometheus metrics** - `ncps_migration_*` metrics
- **Final summary** - Completion report with statistics

**Example output:**

```
INFO starting migration
INFO migration progress found=1523 processed=1523 succeeded=1520 failed=3 elapsed=15s rate=101.53
INFO migration completed found=10000 processed=10000 succeeded=9987 failed=13 duration=98.5s rate=101.52
```

### When to Migrate

**Use background migration when:**

- Running in production with uptime requirements
- Cache has moderate traffic
- No rush to complete migration

**Use CLI migration when:**

- Large cache (millions of narinfos)
- Need faster completion
- Storage space is limited (migration deletes narinfos)
- Upgrading from pre-database versions

See [NarInfo Migration Guide](../Operations/NarInfo%20Migration.md) for comprehensive documentation.

## Best Practices

1. **Set reasonable max-size** - Based on available disk space
1. **Enable LRU cleanup** - Automatic management
1. **Monitor cache usage** - Watch for growth trends
1. **Plan for growth** - Cache size increases over time
1. **Use S3 for large caches** - Better for 1TB+ caches
1. **Migrate narinfo to database** - Improves lookup performance

## Next Steps

- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Track cache performance
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All cache options

## Related Documentation

- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Storage backends
- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Monitor cache metrics
- <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> - Solve issues
