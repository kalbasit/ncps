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

## Best Practices

1. **Set reasonable max-size** - Based on available disk space
1. **Enable LRU cleanup** - Automatic management
1. **Monitor cache usage** - Watch for growth trends
1. **Plan for growth** - Cache size increases over time
1. **Use S3 for large caches** - Better for 1TB+ caches

## Next Steps

- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Track cache performance
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All cache options

## Related Documentation

- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Storage backends
- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Monitor cache metrics
- <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> - Solve issues
