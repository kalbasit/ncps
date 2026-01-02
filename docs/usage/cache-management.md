[Home](../../README.md) > [Documentation](../README.md) > [Usage](README.md) > Cache Management

# Cache Management Guide

Manage cache size, cleanup, and optimization.

## Size Limits

### Configure Maximum Size

**Command-line:**

```bash
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

```bash
du -sh /var/lib/ncps
```

**S3 storage:**

```bash
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

```bash
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

```promql
rate(ncps_nar_served_total[5m])
```

See [Monitoring Guide](../operations/monitoring.md) for dashboards.

## Storage Optimization

### Identify Large Packages

**Local storage:**

```bash
find /var/lib/ncps/nar -type f -size +100M | sort -h
```

**S3 storage:**

```bash
aws s3 ls --recursive s3://ncps-cache/nar/ | sort -k3 -n | tail -20
```

### Delete Specific Packages

**Warning:** Manual deletion not recommended. Use LRU cleanup instead.

If absolutely necessary:

```bash
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

- [Configure Monitoring](../operations/monitoring.md) - Track cache performance
- [Review Configuration](../configuration/reference.md) - All cache options

## Related Documentation

- [Storage Configuration](../configuration/storage.md) - Storage backends
- [Monitoring Guide](../operations/monitoring.md) - Monitor cache metrics
- [Troubleshooting Guide](../operations/troubleshooting.md) - Solve issues
