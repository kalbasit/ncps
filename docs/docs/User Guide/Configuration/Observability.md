# Observability

## Observability Configuration

Configure monitoring, metrics, logging, and tracing for ncps.

## Overview

ncps provides comprehensive observability through:

- **Prometheus** - Metrics endpoint for monitoring your deployment
- **OpenTelemetry** - Distributed tracing and telemetry for your infrastructure
- **Structured Logging** - JSON-formatted logs with context
- **Analytics Reporting** - Anonymous usage statistics sent to project maintainers (separate from your monitoring)

**Important:** This page covers observability for **your own deployment** (Prometheus, OpenTelemetry, logs). For information about anonymous usage statistics sent to project maintainers, see [Analytics Configuration](Analytics.md).

## Prometheus Metrics

### Enable Prometheus

**Command-line:**

```sh
# For the server
ncps serve --prometheus-enabled=true
```

**Configuration file:**

```yaml
prometheus:
  enabled: true
```

**Environment variable:**

```sh
export PROMETHEUS_ENABLED=true
```

### Metrics Endpoint

Once enabled, metrics are available at:

```http
http://your-ncps:8501/metrics
```

### Available Metrics

**HTTP Metrics** (via otelchi middleware):

- `http_server_requests_total` - Total HTTP requests
- `http_server_request_duration_seconds` - Request duration histogram
- `http_server_active_requests` - Currently active requests

**Cache Metrics:**

- `ncps_nar_served_total` - Total NAR files served
- `ncps_narinfo_served_total` - Total NarInfo files served

**Upstream Health Metrics** (available when analytics reporting is enabled):

- `ncps_upstream_count_healthy` - Number of healthy upstream caches
- `ncps_upstream_count_total` - Total number of configured upstream caches

Note: Upstream health metrics are collected as part of analytics reporting. See [Analytics Configuration](Analytics.md) for details.

**Lock Metrics** (when using Redis for HA):

- `ncps_lock_acquisitions_total{type,result,mode}` - Lock acquisition attempts
  - `type`: "download" or "lru"
  - `result`: "success" or "failure"
  - `mode`: "local" or "distributed"
- `ncps_lock_hold_duration_seconds{type,mode}` - Lock hold time histogram
- `ncps_lock_failures_total{type,reason,mode}` - Lock failures
  - `reason`: "timeout", "redis_error", "circuit_breaker"
- `ncps_lock_retry_attempts_total{type}` - Retry attempts

**Migration Metrics** (during narinfo migration):

- `ncps_migration_objects_total{operation,result}` - Total number of objects processed during migration.
  - `operation`: "migrate" or "delete"
  - `result`: "success", "failure", or "skipped"
- `ncps_migration_duration_seconds{operation}` - Migration operation duration histogram
  - `operation`: "migrate" or "delete"
- `ncps_migration_batch_size` - Migration batch size histogram

**Background Migration Metrics** (during on-the-fly migration):

- `ncps_background_migration_narinfos_total{operation,result}` - Background NarInfo migration operations
  - `operation`: "migrate" or "delete"
  - `result`: "success" or "failure"
- `ncps_background_migration_duration_seconds{operation}` - Background migration operation duration histogram
  - `operation`: "migrate" or "delete"

See [NarInfo Migration Guide](../Operations/NarInfo%20Migration.md) for migration documentation.

### Prometheus Configuration

Add ncps to your `prometheus.yml`:

```yaml
scrape_configs:
  - job_name: 'ncps'
    static_configs:
      - targets: ['ncps:8501']
    scrape_interval: 30s
```

**For Kubernetes with ServiceMonitor:**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ncps
  namespace: ncps
spec:
  selector:
    matchLabels:
      app: ncps
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
```

### Grafana Dashboards

Create dashboards to visualize:

**Cache Performance:**

- Cache hit rate
- NAR files served (rate)
- Request duration percentiles (p50, p95, p99)

**HA Lock Performance:**

- Lock acquisition success/failure rate
- Lock hold duration
- Retry attempt rate
- Lock contention

See the [Monitoring Guide](../Operations/Monitoring.md) for dashboard examples.

## OpenTelemetry

### Enable OpenTelemetry

**Command-line:**

```sh
# For the server
ncps serve \
  --otel-enabled=true \
  --otel-grpc-url=http://otel-collector:4317

# For narinfo migration
ncps migrate-narinfo \
  --otel-enabled=true \
  --otel-grpc-url=http://otel-collector:4317
```

**Configuration file:**

```yaml
otel:
  enabled: true
  grpc-url: http://otel-collector:4317
```

**Environment variables:**

```sh
export OTEL_ENABLED=true
export OTEL_GRPC_URL=http://otel-collector:4317
```

### Telemetry Signals

When enabled, OpenTelemetry provides:

**1. Logs** - Structured application logs **2. Metrics** - Application and system metrics **3. Traces** - Distributed request tracing

### OpenTelemetry Collector Setup

Example collector configuration (`otel-collector-config.yaml`):

```yaml
  config:
    exporters:
      otlphttp/loki:
        endpoint: http://loki/otlp
      otlphttp/mimir:
        endpoint: http://mimir/otlp
      otlphttp/tempo:
        endpoint: https://tempo
    processors:
      batch: {}
      k8sattributes:
        passthrough: false
      resource:
        attributes:
          - action: insert
            from_attribute: k8s.deployment.name
            key: service.name
          - action: insert
            from_attribute: k8s.namespace.name
            key: namespace
          - action: insert
            from_attribute: k8s.pod.name
            key: pod
          - action: insert
            key: cluster
            value: pve-cluster-prod0
    receivers:
      otlp:
        protocols:
          grpc:
            endpoint: 0.0.0.0:4317
          http:
            endpoint: 0.0.0.0:4318
    service:
      pipelines:
        logs:
          exporters:
            - otlphttp/loki
          processors:
            - k8sattributes
            - resource
            - batch
          receivers:
            - otlp
        metrics:
          exporters:
            - otlphttp/mimir
          processors:
            - k8sattributes
            - resource
            - batch
          receivers:
            - otlp
        traces:
          exporters:
            - otlphttp/tempo
          processors:
            - k8sattributes
            - resource
            - batch
          receivers:
            - otlp
```

### Stdout Mode

If `--otel-grpc-url` is omitted, telemetry is written to stdout:

```sh
ncps serve --otel-enabled=true
# Logs, metrics, and traces written to stdout in JSON format
```

Useful for development or when using log aggregation systems.

## Logging

### Log Levels

Configure logging verbosity:

**Command-line:**

```sh
ncps serve --log-level=debug
```

**Levels:**

- `debug` - Verbose logging, including debug information
- `info` - Standard informational messages (default)
- `warn` - Warning messages only
- `error` - Error messages only

**Configuration file:**

```yaml
log-level: info
```

### Log Format

Logs are output in JSON format with structured fields:

```
{
  "level": "info",
  "ts": "2024-01-15T10:30:00Z",
  "msg": "acquired download lock",
  "hash": "abc123def456",
  "lock_type": "download:nar",
  "duration_ms": 150,
  "retries": 2
}
```

### Important Log Messages

**Cache Operations:**

- `serving nar from cache` - NAR file served from cache
- `downloading nar from upstream` - Fetching from upstream
- `nar cached successfully` - Download and cache complete

**Lock Operations (HA):**

- `acquired download lock` - Download lock obtained
- `failed to acquire lock` - Lock acquisition failed after retries
- `another instance is running LRU` - LRU skipped (another instance running)
- `circuit breaker open: Redis is unavailable` - Redis connectivity issues

**Server:**

- `server started` - ncps HTTP server started
- `server shutdown` - Graceful shutdown initiated

### Log Aggregation

**ELK Stack (Elasticsearch, Logstash, Kibana):**

```sh
# Send logs to Logstash
ncps serve --log-level=info 2>&1 | logstash -f logstash.conf
```

**Loki + Grafana:**

```yaml
# Docker Compose with Promtail
services:
  ncps:
    # ...
    logging:
      driver: loki
      options:
        loki-url: "http://loki:3100/loki/api/v1/push"
```

**CloudWatch (AWS):**

```
# Use CloudWatch agent to collect logs
# Configure log groups and streams
```

## Distributed Tracing

### Jaeger Setup

With OpenTelemetry and Jaeger, trace requests across multiple ncps instances:

```yaml
# docker-compose.yml
services:
  jaeger:
    image: jaegertracing/all-in-one:latest
    environment:
      COLLECTOR_OTLP_ENABLED: true
    ports:
      - "16686:16686"  # Jaeger UI
      - "14250:14250"  # gRPC

  otel-collector:
    image: otel/opentelemetry-collector:latest
    command: ["--config=/etc/otel-collector-config.yaml"]
    volumes:
      - ./otel-collector-config.yaml:/etc/otel-collector-config.yaml
    ports:
      - "4317:4317"  # OTLP gRPC

  ncps:
    # ...
    environment:
      OTEL_ENABLED: "true"
      OTEL_GRPC_URL: "http://otel-collector:4317"
```

Access Jaeger UI at `http://localhost:16686` to view traces.

### Trace Context

Traces include:

- Request ID
- Upstream cache calls
- Lock acquisitions (HA mode)
- Database queries
- S3 operations
- Download and cache operations

## Health Checks

### Endpoints

**Cache Info:**

```sh
curl http://localhost:8501/nix-cache-info
```

Returns cache metadata in Nix binary cache format.

**Metrics (if Prometheus enabled):**

```sh
curl http://localhost:8501/metrics
```

Returns Prometheus-formatted metrics.

### Health Check Scripts

**Simple health check:**

```
#!/bin/bash
curl -f http://localhost:8501/nix-cache-info || exit 1
```

**Kubernetes liveness probe:**

```yaml
livenessProbe:
  httpGet:
    path: /nix-cache-info
    port: 8501
  initialDelaySeconds: 30
  periodSeconds: 10
```

**Kubernetes readiness probe:**

```yaml
readinessProbe:
  httpGet:
    path: /nix-cache-info
    port: 8501
  initialDelaySeconds: 5
  periodSeconds: 5
```

## Example: Complete Observability Stack

Docker Compose with full observability:

```yaml
services:
  ncps:
    image: kalbasit/ncps:latest
    environment:
      PROMETHEUS_ENABLED: "true"
      OTEL_ENABLED: "true"
      OTEL_GRPC_URL: "http://otel-collector:4317"
      LOG_LEVEL: "info"
    ports:
      - "8501:8501"

  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    ports:
      - "9090:9090"

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      GF_SECURITY_ADMIN_PASSWORD: admin

  otel-collector:
    image: otel/opentelemetry-collector:latest
    command: ["--config=/etc/otel-collector-config.yaml"]
    volumes:
      - ./otel-collector-config.yaml:/etc/otel-collector-config.yaml
    ports:
      - "4317:4317"

  jaeger:
    image: jaegertracing/all-in-one:latest
    environment:
      COLLECTOR_OTLP_ENABLED: true
    ports:
      - "16686:16686"

  loki:
    image: grafana/loki:latest
    ports:
      - "3100:3100"
```

**Access:**

- ncps: `http://localhost:8501`
- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000` (admin/admin)
- Jaeger: `http://localhost:16686`

## Next Steps

1. <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Set up dashboards and alerts
1. <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> - Use logs and metrics to debug
1. <a class="reference-link" href="Reference.md">Reference</a> - All observability options

## Related Documentation

- <a class="reference-link" href="Analytics.md">Analytics</a> - Anonymous usage statistics reporting
- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Detailed monitoring setup
- <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a> - HA observability
- <a class="reference-link" href="Reference.md">Reference</a> - All configuration options
