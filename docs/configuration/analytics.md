[Home](../../README.md) > [Documentation](../README.md) > [Configuration](README.md) > Analytics

# Analytics Reporting

Configure anonymous usage statistics reporting to help improve ncps.

## Overview

ncps includes an optional analytics reporting system that collects anonymous usage statistics and sends them to the project maintainers. This data helps inform development priorities, identify common configurations, and understand how ncps is being used in the wild.

**Key Points:**

- **Enabled by default** - Can be disabled with `--analytics-reporting-enabled=false`
- **Fully anonymous** - No personal data, IP addresses, or cache contents
- **Minimal overhead** - Metrics sent once per hour, logs only on startup and errors
- **Privacy-focused** - Only collects configuration metadata and aggregate statistics

## What Data is Collected?

Analytics reporting collects three types of data:

### 1. Resource Attributes (Metadata)

These attributes are attached to all metrics and logs:

| Attribute | Description | Example Values |
|-----------|-------------|----------------|
| `ncps.db_type` | Database backend type | `sqlite`, `postgres`, `mysql` |
| `ncps.lock_type` | Lock mechanism type | `local`, `redis` |
| `ncps.cluster_uuid` | Unique cluster identifier | `550e8400-e29b-41d4-a716-446655440000` |
| `service.name` | Service name | `ncps` |
| `service.version` | ncps version | `v1.2.3` |

**Note:** The `cluster_uuid` is randomly generated and stored in your database. It helps identify unique installations but contains no identifying information about your organization or infrastructure.

### 2. Metrics (Sent Hourly)

Aggregate statistics about cache usage:

| Metric | Description | Unit |
|--------|-------------|------|
| `ncps_store_nar_files_total_size_bytes` | Total size of all cached NAR files | bytes |
| `ncps_upstream_count_total` | Number of configured upstream caches | count |
| `ncps_upstream_count_healthy` | Number of healthy upstream caches | count |

These metrics are sent every **1 hour** to minimize network overhead.

### 3. Logs

Event logs for application lifecycle:

| Event | When | Data Included |
|-------|------|---------------|
| **Startup** | When ncps starts | Service name, version, resource attributes |
| **Panics** | When application crashes | Panic message, stack trace, timestamp |

**Note:** Panic logs include stack traces to help identify bugs, but they do not contain sensitive data from your environment.

## What is NOT Collected?

Analytics reporting explicitly **does not** collect:

- **Personal information** - No usernames, emails, or other PII
- **Network information** - No IP addresses, hostnames, or network topology
- **Cache contents** - No store paths, package names, or derivation data
- **Configuration secrets** - No passwords, keys, or authentication tokens
- **Request logs** - No HTTP requests, client information, or access patterns
- **Storage paths** - No file paths, bucket names, or storage configuration
- **Upstream URLs** - No upstream cache URLs or authentication details

## Analytics Endpoint

Data is sent to:

```
otlp.ncps.dev:443
```

This endpoint receives OpenTelemetry data over HTTPS using:

- **OTLP/HTTP** protocol
- **gzip compression** to minimize bandwidth
- **TLS encryption** for secure transmission

## Privacy and Security

### Data Retention

Analytics data is retained for:

- **Metrics**: 90 days (for trend analysis)
- **Logs**: 30 days (for bug identification)

After this period, data is automatically deleted.

### Data Usage

Analytics data is used exclusively for:

- Understanding deployment patterns (database types, HA vs single-instance)
- Identifying common cache sizes to inform default configurations
- Detecting bugs through panic logs
- Measuring upstream health trends
- Planning future development priorities

Analytics data is **never**:

- Sold to third parties
- Used for advertising or tracking
- Shared outside the ncps project maintainers
- Used to identify individual users or organizations

### Security

- All data transmission uses TLS encryption
- No authentication tokens or credentials are collected
- Panic logs are sanitized to remove environment variables
- The cluster UUID is randomly generated and cannot be traced back to your organization

## Configuration

### Enable Analytics (Default)

Analytics is enabled by default. No configuration needed.

**Command-line:**

```bash
ncps serve --analytics-reporting-enabled=true
```

**Configuration file:**

```yaml
analytics:
  reporting:
    enabled: true
```

**Environment variable:**

```bash
export ANALYTICS_REPORTING_ENABLED=true
```

### Disable Analytics (Opt-Out)

To disable analytics reporting:

**Command-line:**

```bash
ncps serve --analytics-reporting-enabled=false
```

**Configuration file:**

```yaml
analytics:
  reporting:
    enabled: false
```

**Environment variable:**

```bash
export ANALYTICS_REPORTING_ENABLED=false
```

**Docker example:**

```bash
docker run -it --rm \
  -e ANALYTICS_REPORTING_ENABLED=false \
  kalbasit/ncps:latest serve \
  --cache-hostname=cache.example.com \
  --cache-storage-local=/var/lib/ncps \
  --cache-upstream-url=https://cache.nixos.org \
  --cache-upstream-public-key=cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

**Kubernetes/Helm example:**

```yaml
# values.yaml
analytics:
  reporting:
    enabled: false
```

### Verify Analytics Status

When analytics is enabled, you'll see a log message at startup:

```json
{
  "level": "info",
  "msg": "Reporting anonymous metrics to the project maintainers",
  "endpoint": "otlp.ncps.dev:443"
}
```

When disabled, no message is logged and no data is sent.

## Panic Logging and Recovery

One of the key features of the analytics system is **panic recovery** for background operations.

### How It Works

ncps uses `analytics.SafeGo()` to wrap all background goroutines with panic recovery. When a panic occurs:

1. **Panic is caught** - The goroutine doesn't crash the entire application
1. **Stack trace is captured** - Full stack trace for debugging
1. **Logged locally** - Panic is logged to application logs (always, regardless of analytics setting)
1. **Reported to analytics** - If analytics is enabled, panic is sent to maintainers

This ensures that:

- Your ncps instance stays running even if a background operation fails
- Bugs are reported to maintainers automatically (if analytics enabled)
- You have local logs for your own troubleshooting

### Example Panic Log

**Local application log:**

```json
{
  "level": "error",
  "msg": "Application panic recovered",
  "panic-reason": "runtime error: index out of range",
  "stack-trace": "goroutine 42 [running]:\n..."
}
```

**Analytics log (if enabled):**

Includes the same information plus resource attributes (service version, db_type, etc.) to help maintainers identify patterns.

### SafeGo Usage

When analytics is enabled, the following operations use `SafeGo()` for panic protection:

- **LRU cleanup** - Background cache eviction operations
- **Health checks** - Upstream cache health monitoring
- **Metrics collection** - Periodic statistics gathering

## Troubleshooting

### Analytics Not Working

If you expect analytics to be enabled but don't see the startup message:

1. **Check the flag value:**

   ```bash
   ncps serve --help | grep analytics
   ```

1. **Verify environment variables:**

   ```bash
   echo $ANALYTICS_REPORTING_ENABLED
   ```

1. **Check configuration file:**

   ```bash
   cat config.yaml | grep -A 3 analytics
   ```

1. **Check for errors in logs:**

   ```bash
   journalctl -u ncps | grep -i analytics
   ```

### Network Issues

If analytics fails to connect to `otlp.ncps.dev:443`:

- **Firewall** - Ensure outbound HTTPS (port 443) is allowed
- **Proxy** - OpenTelemetry respects HTTP proxy environment variables
- **DNS** - Verify `otlp.ncps.dev` resolves correctly

Analytics failures are **non-fatal** - if the analytics endpoint is unreachable, ncps will log a warning but continue operating normally.

### Disabling for Air-Gapped Environments

For air-gapped or restricted network environments, disable analytics:

```bash
ncps serve --analytics-reporting-enabled=false
```

This prevents any outbound connections to the analytics endpoint.

## Comparison with OpenTelemetry

ncps supports both **analytics reporting** and **OpenTelemetry (OTEL)**:

| Feature | Analytics Reporting | OpenTelemetry |
|---------|---------------------|---------------|
| **Purpose** | Help ncps maintainers | Monitor your own deployment |
| **Destination** | `otlp.ncps.dev:443` | Your OTEL collector |
| **Data** | Minimal (DB type, cache size) | Comprehensive (all metrics, traces, logs) |
| **Default** | Enabled | Disabled |
| **Configuration** | `--analytics-reporting-enabled` | `--otel-enabled`, `--otel-grpc-url` |

You can enable both, either, or neither:

```bash
# Both enabled (monitor your own deployment AND help maintainers)
ncps serve \
  --analytics-reporting-enabled=true \
  --otel-enabled=true \
  --otel-grpc-url=http://otel-collector:4317

# Only OpenTelemetry (monitor your deployment, no analytics)
ncps serve \
  --analytics-reporting-enabled=false \
  --otel-enabled=true \
  --otel-grpc-url=http://otel-collector:4317

# Only analytics (help maintainers, no local monitoring)
ncps serve \
  --analytics-reporting-enabled=true

# Neither (no telemetry at all)
ncps serve \
  --analytics-reporting-enabled=false
```

See [Observability Configuration](observability.md) for details on OpenTelemetry.

## Why Analytics is Enabled by Default

Analytics is enabled by default because:

1. **Low overhead** - Minimal network usage (1 request per hour)
1. **Privacy-focused** - No sensitive data collection
1. **Helpful to maintainers** - Informs development priorities
1. **Easy opt-out** - Single flag to disable

This follows the model of other privacy-respecting open source projects like Homebrew, which collect anonymous statistics to understand usage patterns without compromising user privacy.

## Frequently Asked Questions

### Does analytics impact performance?

No. Analytics has negligible performance impact:

- Metrics sent once per hour (not per request)
- Uses background goroutines (non-blocking)
- Compressed data (minimal bandwidth)

### What if the analytics endpoint is down?

If `otlp.ncps.dev` is unreachable:

- ncps logs a warning but continues operating
- No retries or queuing (to avoid memory buildup)
- Analytics automatically resumes when endpoint is available

### Can I host my own analytics collector?

Not currently. The analytics endpoint is hardcoded to `otlp.ncps.dev:443`. If you need to send telemetry to your own infrastructure, use OpenTelemetry instead (see [Observability Configuration](observability.md)).

### How is this different from telemetry spyware?

Unlike telemetry systems that collect invasive data, ncps analytics:

- **Is transparent** - This documentation explains exactly what's collected
- **Is minimal** - Only configuration metadata and aggregate statistics
- **Is optional** - Easy one-flag opt-out
- **Respects privacy** - No PII, IP addresses, or tracking
- **Has retention limits** - Data automatically deleted after 30-90 days
- **Is open source** - You can review the code in `pkg/analytics/`

## Related Documentation

- [Configuration Reference](reference.md) - All configuration options
- [Observability Configuration](observability.md) - OpenTelemetry and Prometheus
- [Privacy Policy](../../README.md#privacy) - Project privacy policy (if available)

## Feedback

Have questions or concerns about analytics? Please:

- Open an issue: [github.com/kalbasit/ncps/issues](https://github.com/kalbasit/ncps/issues)
- Start a discussion: [github.com/kalbasit/ncps/discussions](https://github.com/kalbasit/ncps/discussions)

Your feedback helps us balance transparency, privacy, and useful data collection.
