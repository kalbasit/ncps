# ncps Helm Chart

A Helm chart for deploying [ncps (Nix Cache Proxy Server)](https://github.com/kalbasit/ncps) on Kubernetes.

## Introduction

This chart bootstraps a ncps deployment on a Kubernetes cluster using the Helm package manager. ncps is a local binary cache proxy for Nix that fetches store paths from upstream caches and caches them locally, reducing download times and bandwidth usage.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.8+
- PV provisioner support in the underlying infrastructure (for local storage with persistence)

## Installation

### Install from OCI Registry

```bash
# Install with default values (single instance, local storage, SQLite)
helm install ncps oci://ghcr.io/kalbasit/helm/ncps --version <chart-version>

# Install with custom values
helm install ncps oci://ghcr.io/kalbasit/helm/ncps \
  --version <chart-version> \
  --set config.hostname=cache.example.com \
  --set config.upstream.caches[0].url=https://cache.nixos.org
```

### Install from Source

```bash
git clone https://github.com/kalbasit/ncps.git
cd ncps/charts/ncps
helm install ncps . -f values.yaml
```

## Upgrading

```bash
# Upgrade to a new version
helm upgrade ncps oci://ghcr.io/kalbasit/helm/ncps --version <new-chart-version>

# Upgrade with new values
helm upgrade ncps oci://ghcr.io/kalbasit/helm/ncps \
  --version <new-chart-version> \
  --set config.cache.maxSize=500G
```

## Uninstalling

```bash
helm uninstall ncps

# To also delete PVCs (persistent volumes)
kubectl delete pvc -l app.kubernetes.io/instance=<release-name>
```

## Configuration

The following table lists the configurable parameters of the ncps chart and their default values.

### Global Settings

| Parameter | Description | Default |
| ---------------------- | ----------------------------------------------------- | --------------- |
| `global.imageRegistry` | Global image registry override | `""` |
| `replicaCount` | Number of replicas (1 for single instance, 2+ for HA) | `1` |
| `mode` | Deployment mode: `deployment` or `statefulset` | `statefulset` |
| `image.registry` | Image registry | `docker.io` |
| `image.repository` | Image repository | `kalbasit/ncps` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `image.tag` | Image tag (defaults to chart appVersion) | `""` |
| `imagePullSecrets` | Image pull secrets | `[]` |
| `nameOverride` | Override chart name | `""` |
| `fullnameOverride` | Override full name | `""` |

### Init Image

| Parameter | Description | Default |
| ----------------------------- | --------------------------------------------- | --------------- |
| `initImage.registry` | Init image registry | `docker.io` |
| `initImage.repository` | Init image repository | `busybox` |
| `initImage.tag` | Init image tag | `1.37.0` |
| `initImage.pullPolicy` | Init image pull policy | `IfNotPresent` |
| `initImage.resources` | Resources for init container | `{}` |

### Service Account

| Parameter | Description | Default |
| --------------------------------------------- | ------------------------------- | ------- |
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.annotations` | Service account annotations | `{}` |
| `serviceAccount.name` | Service account name | `""` |
| `serviceAccount.automountServiceAccountToken` | Automount service account token | `false` |

### Security Context

| Parameter | Description | Default |
| ------------------------------------------ | -------------------------- | -------------------- |
| `podSecurityContext.seccompProfile.type` | Seccomp profile type | `RuntimeDefault` |
| `podSecurityContext.runAsNonRoot` | Run as non-root | `true` |
| `podSecurityContext.runAsUser` | User ID | `1000` |
| `podSecurityContext.runAsGroup` | Group ID | `1000` |
| `podSecurityContext.fsGroup` | FS group | `1000` |
| `podSecurityContext.fsGroupChangePolicy` | FS group change policy | `OnRootMismatch` |
| `securityContext.allowPrivilegeEscalation` | Allow privilege escalation | `false` |
| `securityContext.capabilities.drop` | Dropped capabilities | `[ALL]` |
| `securityContext.readOnlyRootFilesystem` | Read-only root filesystem | `true` |

### ncps Configuration

| Parameter | Description | Default |
| -------------------------------- | -------------------------------- | ------------------ |
| `config.hostname` | Cache hostname (REQUIRED) | `ncps.example.com` |
| `config.logLevel` | Logging level | `info` |
| `config.server.addr` | Server listen address | `:8501` |
| `config.permissions.allowPut` | Allow PUT requests | `false` |
| `config.permissions.allowDelete` | Allow DELETE requests | `false` |
| `config.signing.enabled` | Enable NAR signing | `true` |
| `config.signing.secretKey` | Signing secret key | `""` |
| `config.signing.existingSecret` | Existing secret with signing key | `""` |

### Cache Management

| Parameter | Description | Default |
| -------------------------- | --------------------------------- | ----------- |
| `config.cache.maxSize` | Maximum cache size (e.g., "100G") | `""` |
| `config.cache.lruSchedule` | LRU cleanup cron schedule | `""` |
| `config.cache.lruTimezone` | Timezone for cron schedule | `Local` |
| `config.cache.tempPath` | Temporary directory path | `/tmp/ncps` |

### Storage Configuration

| Parameter | Description | Default |
| --------------------------------------------------- | ------------------------------------------- | ----------------- |
| `config.storage.type` | Storage type: `local` or `s3` | `local` |
| `config.storage.local.path` | Local storage path | `/storage` |
| `config.storage.local.persistence.enabled` | Enable persistent storage | `true` |
| `config.storage.local.persistence.existingClaim` | Use existing PVC | `""` |
| `config.storage.local.persistence.storageClassName` | Storage class | `""` |
| `config.storage.local.persistence.accessModes` | Access modes | `[ReadWriteOnce]` |
| `config.storage.local.persistence.size` | PVC size | `20Gi` |
| `config.storage.local.persistence.annotations` | PVC annotations | `{}` |
| `config.storage.local.persistence.selector` | PV selector | `{}` |
| `config.storage.s3.bucket` | S3 bucket name | `""` |
| `config.storage.s3.endpoint` | S3 endpoint with scheme (https:// or http://) | `""` |
| `config.storage.s3.region` | S3 region | `us-east-1` |
| `config.storage.s3.forcePathStyle` | Force path-style addressing (required for MinIO) | `false` |
| `config.storage.s3.accessKeyId` | S3 access key ID | `""` |
| `config.storage.s3.secretAccessKey` | S3 secret access key | `""` |
| `config.storage.s3.existingSecret` | Existing secret with S3 credentials | `""` |

### Database Configuration

| Parameter | Description | Default |
| ------------------------------------------- | ------------------------------------------------------------ | --------------------- |
| `config.database.type` | Database type: `sqlite`, `postgresql`, or `mysql` | `sqlite` |
| `config.database.sqlite.path` | SQLite database file path | `/storage/db/ncps.db` |
| `config.database.postgresql.existingSecret` | Existing secret with full connection string (key: `database-url`) | `""` |
| `config.database.postgresql.host` | PostgreSQL host | `""` |
| `config.database.postgresql.port` | PostgreSQL port | `5432` |
| `config.database.postgresql.database` | PostgreSQL database name | `ncps` |
| `config.database.postgresql.username` | PostgreSQL username | `ncps` |
| `config.database.postgresql.password` | PostgreSQL password (chart builds connection string) | `""` |
| `config.database.postgresql.sslMode` | PostgreSQL SSL mode | `disable` |
| `config.database.postgresql.extraParams` | Additional connection parameters | `""` |
| `config.database.mysql.existingSecret` | Existing secret with full connection string (key: `database-url`) | `""` |
| `config.database.mysql.host` | MySQL host | `""` |
| `config.database.mysql.port` | MySQL port | `3306` |
| `config.database.mysql.database` | MySQL database name | `ncps` |
| `config.database.mysql.username` | MySQL username | `ncps` |
| `config.database.mysql.password` | MySQL password (chart builds connection string) | `""` |
| `config.database.mysql.extraParams` | Additional connection parameters | `""` |
| `config.database.pool.maxOpenConns` | Maximum open connections (0 = unlimited) | `0` |
| `config.database.pool.maxIdleConns` | Maximum idle connections | `0` |

### Upstream Cache Configuration

| Parameter | Description | Default |
| --------------------------------------- | ------------------------------- | --------------- |
| `config.upstream.caches` | List of upstream caches | See values.yaml |
| `config.upstream.dialerTimeout` | Connection timeout | `3s` |
| `config.upstream.responseHeaderTimeout` | Response header timeout | `3s` |
| `config.upstream.netrcFile` | Netrc file content | `""` |
| `config.upstream.existingNetrcSecret` | Existing secret with netrc file | `""` |

### Redis Configuration (HA Mode)

| Parameter | Description | Default |
| -------------------------------------- | -------------------------------------- | -------------- |
| `config.redis.enabled` | Enable Redis (required for HA) | `false` |
| `config.redis.addresses` | Redis server addresses | `[redis:6379]` |
| `config.redis.username` | Redis username | `""` |
| `config.redis.password` | Redis password | `""` |
| `config.redis.existingSecret` | Existing secret with Redis credentials | `""` |
| `config.redis.db` | Redis database number | `0` |
| `config.redis.useTLS` | Use TLS | `false` |
| `config.redis.poolSize` | Connection pool size | `10` |

### Lock Configuration

| Parameter | Description | Default |
| -------------------------------------------- | ------------------------- | ------- |
| `config.lock.redis.keyPrefix` | Lock key prefix | `ncps:lock:` |
| `config.lock.downloadTTL` | Download lock TTL | `5m` |
| `config.lock.lruTTL` | LRU lock TTL | `30m` |
| `config.lock.retry.maxAttempts` | Maximum retry attempts | `3` |
| `config.lock.retry.initialDelay` | Initial retry delay | `100ms` |
| `config.lock.retry.maxDelay` | Maximum retry delay | `2s` |
| `config.lock.retry.jitter` | Enable retry jitter | `true` |
| `config.lock.allowDegradedMode` | Allow degraded mode | `false` |

### Observability

| Parameter | Description | Default |
| -------------------------------------------- | ------------------------- | ------- |
| `config.observability.opentelemetry.enabled` | Enable OpenTelemetry | `false` |
| `config.observability.opentelemetry.grpcURL` | OTLP gRPC collector URL | `""` |
| `config.observability.prometheus.enabled` | Enable Prometheus metrics | `false` |

### Service Configuration

| Parameter | Description | Default |
| ------------------------- | ------------------- | ----------- |
| `service.type` | Service type | `ClusterIP` |
| `service.port` | Service port | `8501` |
| `service.portName` | Port name | `http` |
| `service.annotations` | Service annotations | `{}` |
| `service.sessionAffinity` | Session affinity | `None` |

### Ingress Configuration

| Parameter | Description | Default |
| --------------------- | ------------------------- | --------------- |
| `ingress.enabled` | Enable Ingress | `false` |
| `ingress.className` | Ingress class name | `nginx` |
| `ingress.annotations` | Ingress annotations | `{}` |
| `ingress.hosts` | Ingress hosts | See values.yaml |
| `ingress.tls` | Ingress TLS configuration | `[]` |

### Health Checks

| Parameter | Description | Default |
| ---------------- | ----------------------------- | --------------- |
| `livenessProbe` | Liveness probe configuration | See values.yaml |
| `readinessProbe` | Readiness probe configuration | See values.yaml |

### Resources

| Parameter | Description | Default |
| --------------------------- | -------------- | ------- |
| `resources.limits.cpu` | CPU limit | `2000m` |
| `resources.limits.memory` | Memory limit | `2Gi` |
| `resources.requests.cpu` | CPU request | `100m` |
| `resources.requests.memory` | Memory request | `256Mi` |

### Monitoring

| Parameter | Description | Default |
| ------------------------------ | -------------------------------- | ------- |
| `serviceMonitor.enabled` | Enable Prometheus ServiceMonitor | `false` |
| `serviceMonitor.namespace` | ServiceMonitor namespace | `""` |
| `serviceMonitor.labels` | Additional labels | `{}` |
| `serviceMonitor.interval` | Scrape interval | `30s` |
| `serviceMonitor.scrapeTimeout` | Scrape timeout | `10s` |

### Other Settings

| Parameter | Description | Default |
| ---------------------------------- | --------------------------- | ------- |
| `podDisruptionBudget.enabled` | Enable PodDisruptionBudget | `false` |
| `podDisruptionBudget.minAvailable` | Minimum available pods | `1` |
| `nodeSelector` | Node selector | `{}` |
| `tolerations` | Pod tolerations | `[]` |
| `affinity` | Pod affinity | `{}` |
| `extraEnvVars` | Extra environment variables | `[]` |
| `extraVolumes` | Extra volumes | `[]` |
| `extraVolumeMounts` | Extra volume mounts | `[]` |
| `initContainers` | Init containers | `[]` |
| `sidecars` | Sidecar containers | `[]` |
| `tests.enabled` | Enable Helm tests | `false` |

### Database Migration

| Parameter | Description | Default |
| ---------------------------------------------- | ------------------------------------------------- | --------------- |
| `migration.enabled` | Enable database migration | `false` |
| `migration.mode` | Migration mode: `initContainer`, `job`, `argocd` | `initContainer` |
| `migration.resources` | Resources for migration container/job | `{}` |
| `migration.securityContext` | Security context for migration container/job | See values.yaml |
| `migration.job.backoffLimit` | Job backoff limit | `3` |
| `migration.job.ttlSecondsAfterFinished` | Job TTL after finish (seconds) | `300` |
| `migration.job.annotations` | Job annotations | `{}` |
| `migration.job.nodeSelector` | Node selector for migration job | `{}` |
| `migration.job.tolerations` | Tolerations for migration job | `[]` |
| `migration.job.affinity` | Affinity for migration job | `{}` |

## Examples

### Single Instance with Local Storage

Default configuration - simple single-instance deployment:

```yaml
replicaCount: 1
mode: statefulset
config:
  hostname: cache.example.com
  storage:
    type: local
    local:
      persistence:
        enabled: true
        size: 50Gi
  database:
    type: sqlite
```

### Single Instance with S3 and PostgreSQL

Production single-instance with S3 storage and PostgreSQL:

```yaml
replicaCount: 1
mode: statefulset
config:
  hostname: cache.example.com
  storage:
    type: s3
    s3:
      bucket: my-nix-cache
      endpoint: https://s3.amazonaws.com
      region: us-west-2
      accessKeyId: AKIA...
      secretAccessKey: secret...
  database:
    type: postgresql
    postgresql:
      host: postgres.example.com
      port: 5432
      database: ncps
      username: ncps
      password: secretpassword
```

### High Availability with 3 Replicas

HA deployment with S3, PostgreSQL, and Redis:

```yaml
replicaCount: 3
mode: deployment
config:
  hostname: cache.example.com
  storage:
    type: s3
    s3:
      bucket: my-nix-cache-ha
      endpoint: https://s3.amazonaws.com
      region: us-west-2
      accessKeyId: AKIA...
      secretAccessKey: secret...
  database:
    type: postgresql
    postgresql:
      host: postgres-ha.example.com
      port: 5432
      database: ncps
      username: ncps
      password: secretpassword
  redis:
    enabled: true
    addresses:
      - redis-ha:6379
    password: redispassword
    lock:
      downloadTTL: 5m
      lruTTL: 30m

affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchExpressions:
              - key: app.kubernetes.io/name
                operator: In
                values:
                  - ncps
          topologyKey: kubernetes.io/hostname
```

### HA with Shared Filesystem (NFS)

HA deployment using StatefulSet with NFS for storage:

```yaml
replicaCount: 3
mode: statefulset
config:
  hostname: cache.example.com
  storage:
    type: local
    local:
      path: /storage
      persistence:
        enabled: true
        storageClassName: nfs-client # NFS storage class
        size: 100Gi
        accessModes:
          - ReadWriteMany # NFS supports multiple writers
  database:
    type: postgresql
    postgresql:
      host: postgres-ha.example.com
      port: 5432
      database: ncps
      username: ncps
      password: secretpassword
  redis:
    enabled: true
    addresses:
      - redis-ha:6379
    password: redispassword
```

### Using Existing Secrets

For better security, use existing Kubernetes secrets:

```yaml
config:
  storage:
    type: s3
    s3:
      bucket: my-nix-cache
      endpoint: s3.amazonaws.com
      region: us-west-2
      existingSecret: ncps-s3-credentials
  database:
    type: postgresql
    postgresql:
      existingSecret: ncps-postgres-credentials
  redis:
    enabled: true
    addresses:
      - redis:6379
    existingSecret: ncps-redis-credentials
  signing:
    existingSecret: ncps-signing-key
```

Create secrets:

```bash
# S3 credentials
kubectl create secret generic ncps-s3-credentials \
  --from-literal=access-key-id=AKIA... \
  --from-literal=secret-access-key=secret...

# PostgreSQL connection string
kubectl create secret generic ncps-postgres-credentials \
  --from-literal=database-url="postgresql://ncps:secretpassword@postgres.example.com:5432/ncps?sslmode=disable"

# Redis credentials
kubectl create secret generic ncps-redis-credentials \
  --from-literal=password=redispassword

# Signing key
kubectl create secret generic ncps-signing-key \
  --from-literal=signing-key="ncps-1:base64encodedkey..."
```

Alternatively, for PostgreSQL and MySQL you can use individual connection parameters and let the chart build the connection string:

```yaml
config:
  database:
    type: postgresql
    postgresql:
      host: postgres.example.com
      port: 5432
      database: ncps
      username: ncps
      password: secretpassword
      sslMode: disable
      extraParams: "connect_timeout=10"
```

### With Ingress and TLS

Expose ncps via Ingress with Let's Encrypt TLS:

```yaml
config:
  hostname: cache.example.com

ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "0" # Allow large uploads
  hosts:
    - host: cache.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: ncps-tls
      hosts:
        - cache.example.com
```

### With Prometheus Monitoring

Enable Prometheus metrics and ServiceMonitor:

```yaml
config:
  observability:
    prometheus:
      enabled: true

serviceMonitor:
  enabled: true
  interval: 30s
  scrapeTimeout: 10s
  labels:
    prometheus: kube-prometheus
```

## Testing

The chart includes a connection test that verifies the service is responding on the `/healthz` endpoint.

### Using Native Helm

When deploying with `helm install` or `helm upgrade`, you can enable and run tests:

```bash
# Install with tests enabled
helm install ncps oci://ghcr.io/kalbasit/helm/ncps \
  --set tests.enabled=true

# Run the test
helm test ncps
```

### Using `helm template` (nixidy, manual templating, etc.)

**IMPORTANT**: Keep `tests.enabled=false` (the default) when using `helm template`.

When using `helm template` (manually or via tools like nixidy), Helm hook annotations are ignored and the test pod gets rendered as a regular resource, causing issues:

- The pod runs once and completes
- It appears as degraded/failed in your cluster
- Updates to the pod fail (can't update completed pods)

```yaml
# values.yaml when using helm template
tests:
  enabled: false # Keep disabled to prevent test pod from being rendered
```

**Note for ArgoCD/Flux users**: These tools may use `helm template` internally. Check your configuration:

- ArgoCD: Depends on your Application's source configuration
- Flux: HelmRelease with `spec.install.disableHooks: true` has this issue

To test your deployment when using templating, use the readiness/liveness probes or manually verify:

```bash
kubectl get pod -l app.kubernetes.io/instance=<release-name>
kubectl port-forward svc/<release-name>-ncps 8501:8501
curl http://localhost:8501/healthz
```

## Troubleshooting

### Chart Validation Errors

The chart includes validation to prevent incompatible configurations:

**Error: "High availability mode requires Redis"**

- Enable Redis when using multiple replicas: `config.redis.enabled=true`

**Error: "High availability mode is not compatible with SQLite"**

- Use PostgreSQL or MySQL: `config.database.type=postgresql`

**Error: "High availability mode with Deployment requires S3 storage"**

- Either use S3: `config.storage.type=s3`
- Or switch to StatefulSet with NFS: `mode=statefulset`

### Pods Not Starting

Check pod events:

```bash
kubectl describe pod -l app.kubernetes.io/instance=<release-name>
```

Check logs:

```bash
kubectl logs -l app.kubernetes.io/instance=<release-name>
```

### Storage Issues

Verify PVC status:

```bash
kubectl get pvc -l app.kubernetes.io/instance=<release-name>
```

For NFS issues, verify storage class supports ReadWriteMany:

```bash
kubectl get storageclass
```

### Database Connection Failures

Test database connectivity:

```bash
kubectl exec -it deployment/<release-name>-ncps -- /bin/sh
# Try connecting to database
```

Verify database credentials in secrets:

```bash
kubectl get secret <release-name>-ncps -o yaml
```

### Redis Connection Issues

Verify Redis is accessible:

```bash
kubectl exec -it deployment/<release-name>-ncps -- redis-cli -h redis -p 6379 ping
```

Check Redis authentication:

```bash
kubectl get secret <release-name>-ncps -o jsonpath='{.data.redis-password}' | base64 -d
```

## Further Information

- [ncps GitHub Repository](https://github.com/kalbasit/ncps)
- [ncps Documentation](https://github.com/kalbasit/ncps/blob/main/README.md)
- [Distributed Locking Guide](https://github.com/kalbasit/ncps/blob/main/docs/distributed-locking.md)
- [Helm Documentation](https://helm.sh/docs/)
