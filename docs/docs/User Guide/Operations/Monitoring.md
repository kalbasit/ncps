# Monitoring

## Monitoring Guide

Set up monitoring, metrics, and alerting for ncps.

## Enable Prometheus

Enable the `/metrics` endpoint (supported by `serve` and `migrate-narinfo` commands):

```yaml
prometheus:
  enabled: true
```

Access metrics at: `http://your-ncps:8501/metrics` (for `serve`) or via stdout/OTel (for `migrate-narinfo`).

## Available Metrics

**HTTP Metrics:**

- `http_server_requests_total` - Total HTTP requests
- `http_server_request_duration_seconds` - Request duration
- `http_server_active_requests` - Active requests

**Cache Metrics:**

- `ncps_nar_served_total` - NAR files served
- `ncps_narinfo_served_total` - NarInfo files served

**Lock Metrics (HA):**

- `ncps_lock_acquisitions_total{type,result,mode}` - Lock acquisitions
- `ncps_lock_hold_duration_seconds{type,mode}` - Lock hold time
- `ncps_lock_failures_total{type,reason,mode}` - Lock failures

**Migration Metrics:**

- `ncps_migration_objects_total{migration_type,operation,result}` - Total number of objects processed during migration.
  - `migration_type`: "narinfo-to-db"
  - `operation`: "migrate" or "delete"
  - `result`: "success", "failure", or "skipped"
- `ncps_migration_duration_seconds{migration_type,operation}` - Migration operation duration histogram
  - `migration_type`: "narinfo-to-db"
  - `operation`: "migrate" or "delete"
- `ncps_migration_batch_size{migration_type}` - Migration batch size histogram
  - `migration_type`: "narinfo-to-db"

**Background Migration Metrics:**

- `ncps_background_migration_objects_total{migration_type,operation,result}` - Total number of objects processed during background migration.
  - `migration_type`: "narinfo-to-db"
  - `operation`: "migrate" or "delete"
  - `result`: "success" or "failure"
- `ncps_background_migration_duration_seconds{migration_type,operation}` - Background migration operation duration histogram
  - `migration_type`: "narinfo-to-db"
  - `operation`: "migrate" or "delete"

## Prometheus Configuration

Add to `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'ncps'
    static_configs:
      - targets: ['ncps:8501']
    scrape_interval: 30s
```

## Grafana Dashboards

### Key Panels

**Cache Performance:**

- Cache hit rate
- NAR served rate
- Request duration (p50, p95, p99)

**HA Lock Performance:**

- Lock acquisition success rate
- Lock retry attempts
- Lock hold duration

### Example PromQL Queries

**Cache hit rate:**

```
rate(ncps_nar_served_total[5m])
```

**Lock success rate:**

```
rate(ncps_lock_acquisitions_total{result="success"}[5m])
/ rate(ncps_lock_acquisitions_total[5m])
```

**Migration throughput:**

```
rate(ncps_migration_narinfos_total[5m])
```

**Migration success rate:**

```
sum(rate(ncps_migration_narinfos_total{result="success"}[5m]))
/ sum(rate(ncps_migration_narinfos_total[5m]))
```

**Migration duration (p50, p99):**

```
# Median
histogram_quantile(0.5, ncps_migration_duration_seconds)

# 99th percentile
histogram_quantile(0.99, ncps_migration_duration_seconds)
```

## Alerting

### Recommended Alerts

**High Lock Failure Rate:**

```yaml
- alert: HighLockFailureRate
  expr: rate(ncps_lock_failures_total[5m]) > 0.1
  annotations:
    summary: High lock failure rate
```

**ncps Down:**

```yaml
- alert: NcpsDown
  expr: up{job="ncps"} == 0
  for: 1m
  annotations:
    summary: ncps instance down
```

## Health Checks

**Endpoint:** `GET /nix-cache-info`

**Example check:**

```sh
curl -f http://localhost:8501/nix-cache-info || exit 1
```

## Related Documentation

- <a class="reference-link" href="../Configuration/Observability.md">Observability</a> - Configure metrics
- <a class="reference-link" href="Troubleshooting.md">Troubleshooting</a> - Debug issues
