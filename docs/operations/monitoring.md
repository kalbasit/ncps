[Home](../../README.md) > [Documentation](../README.md) > [Operations](README.md) > Monitoring

# Monitoring Guide

Set up monitoring, metrics, and alerting for ncps.

## Enable Prometheus

Enable the `/metrics` endpoint:

```yaml
prometheus:
  enabled: true
```

Access metrics at: `http://your-ncps:8501/metrics`

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
```promql
rate(ncps_nar_served_total[5m])
```

**Lock success rate:**
```promql
rate(ncps_lock_acquisitions_total{result="success"}[5m])
/ rate(ncps_lock_acquisitions_total[5m])
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
```bash
curl -f http://localhost:8501/nix-cache-info || exit 1
```

## Related Documentation

- [Observability Configuration](../configuration/observability.md) - Configure metrics
- [Troubleshooting Guide](troubleshooting.md) - Debug issues
