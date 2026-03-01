# Helm Chart

Install ncps on Kubernetes using Helm for simplified configuration and management. This is the recommended method for production Kubernetes deployments.

## Prerequisites

- Kubernetes 1.19+
- Helm 3.8+
- kubectl configured and connected to your cluster
- PV provisioner support in the underlying infrastructure (for local storage with persistence)

## Quick Start

### Install from OCI Registry

```sh
# Create namespace
kubectl create namespace ncps

# Install the chart from OCI registry
helm install ncps oci://ghcr.io/kalbasit/helm/ncps \
  --version <chart-version> \
  --namespace ncps \
  --set config.hostname=cache.example.com
```

### Install from Source

```sh
git clone https://github.com/kalbasit/ncps.git
cd ncps/charts/ncps
helm install ncps . -f values.yaml --namespace ncps
```

This installs ncps with:

- Single replica (StatefulSet mode)
- Local storage (PersistentVolumeClaim, 20Gi)
- SQLite database
- Default upstream: cache.nixos.org

### Verify Installation

```sh
# Check pod status
kubectl -n ncps get pods

# Check service
kubectl -n ncps get svc

# Get public key
kubectl -n ncps run test --rm -it --image=curlimages/curl -- \
  curl http://ncps:8501/pubkey
```

## Deployment Modes

The chart supports two deployment modes:

### StatefulSet Mode (Default)

Best for:

- Single instance deployments
- Local persistent storage
- SQLite database

```yaml
mode: statefulset
replicaCount: 1
config:
  storage:
    type: local
    local:
      persistence:
        enabled: true
        size: 50Gi
  database:
    type: sqlite
```

### Deployment Mode

Best for:

- High availability (multiple replicas)
- S3 storage
- PostgreSQL/MySQL database

```yaml
mode: deployment
replicaCount: 2
config:
  storage:
    type: s3
  database:
    type: postgresql
  redis:
    enabled: true
```

## Configuration Examples

### Minimal Configuration

```yaml
config:
  hostname: cache.example.com
```

### Single Instance with Local Storage

```yaml
mode: statefulset
replicaCount: 1

config:
  hostname: cache.example.com

  storage:
    type: local
    local:
      persistence:
        enabled: true
        size: 100Gi
        storageClassName: fast-ssd

  database:
    type: sqlite

  cdc:
    enabled: true
```

### Single Instance with S3 Storage

```yaml
mode: deployment
replicaCount: 1

config:
  hostname: cache.example.com

  storage:
    type: s3
    s3:
      bucket: my-ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      accessKeyId: YOUR_ACCESS_KEY
      secretAccessKey: YOUR_SECRET_KEY

  database:
    type: postgresql
    postgresql:
      host: postgres.database.svc.cluster.local
      port: 5432
      database: ncps
      username: ncps
      password: YOUR_DB_PASSWORD
```

### High Availability with PostgreSQL

```yaml
mode: deployment
replicaCount: 3

config:
  hostname: cache.example.com

  storage:
    type: s3
    s3:
      bucket: my-ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      accessKeyId: YOUR_ACCESS_KEY
      secretAccessKey: YOUR_SECRET_KEY

  database:
    type: postgresql
    postgresql:
      host: postgres.database.svc.cluster.local
      port: 5432
      database: ncps
      username: ncps
      password: YOUR_DB_PASSWORD
      sslMode: require

  redis:
    enabled: true
    addresses:
      - redis-master.redis.svc.cluster.local:6379
    password: YOUR_REDIS_PASSWORD
    db: 0
    useTLS: false
```

### Using Existing Secrets

For better security, use Kubernetes secrets instead of values in plain text:

```yaml
config:
  storage:
    type: s3
    s3:
      bucket: my-ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      existingSecret: ncps-s3-credentials  # Secret with access-key-id and secret-access-key

  database:
    type: postgresql
    postgresql:
      existingSecret: ncps-db-credentials  # Secret with "database-url" key (full connection string)

  redis:
    enabled: true
    addresses:
      - redis.redis.svc.cluster.local:6379
    existingSecret: ncps-redis-credentials  # Secret with "password" and optionally "username" keys

  signing:
    existingSecret: ncps-signing-key  # Secret with "signing-key" key
```

Create the secrets:

```sh
# S3 credentials
kubectl create secret generic ncps-s3-credentials -n ncps \
  --from-literal=access-key-id=YOUR_ACCESS_KEY \
  --from-literal=secret-access-key=YOUR_SECRET_KEY

# Database connection string
kubectl create secret generic ncps-db-credentials -n ncps \
  --from-literal=database-url="postgresql://ncps:YOUR_DB_PASSWORD@postgres.database.svc.cluster.local:5432/ncps?sslmode=require"

# Redis password
kubectl create secret generic ncps-redis-credentials -n ncps \
  --from-literal=password=YOUR_REDIS_PASSWORD

# Signing key (optional, auto-generated if not provided)
kubectl create secret generic ncps-signing-key -n ncps \
  --from-literal=signing-key=YOUR_SIGNING_KEY
```

Alternatively, for PostgreSQL and MySQL you can use individual connection parameters and let the chart build the connection string:

```yaml
config:
  database:
    type: postgresql
    postgresql:
      host: postgres.database.svc.cluster.local
      port: 5432
      database: ncps
      username: ncps
      password: YOUR_DB_PASSWORD
      sslMode: require
      extraParams: "connect_timeout=10"
```

## Database Configuration

### SQLite (Default)

```yaml
config:
  database:
    type: sqlite
    sqlite:
      path: /storage/db/ncps.db
```

**Note:** SQLite always requires persistent storage, even when using S3 for NAR files.

### PostgreSQL

```yaml
config:
  database:
    type: postgresql
    postgresql:
      # Option 1: Use existing secret with full connection string
      existingSecret: ncps-db-credentials  # Secret with "database-url" key

      # Option 2: Use individual connection parameters (chart builds connection string)
      # host: postgres.database.svc.cluster.local
      # port: 5432
      # database: ncps
      # username: ncps
      # password: YOUR_DB_PASSWORD
      # sslMode: disable
      # extraParams: "connect_timeout=10"  # Additional connection parameters

    # Connection pool settings
    pool:
      maxOpenConns: 25
      maxIdleConns: 5
```

### MySQL/MariaDB

```yaml
config:
  database:
    type: mysql
    mysql:
      # Option 1: Use existing secret with full connection string
      existingSecret: ncps-db-credentials  # Secret with "database-url" key

      # Option 2: Use individual connection parameters (chart builds connection string)
      # host: mysql.database.svc.cluster.local
      # port: 3306
      # database: ncps
      # username: ncps
      # password: YOUR_DB_PASSWORD
      # extraParams: "timeout=10s"  # Additional connection parameters

    # Connection pool settings
    pool:
      maxOpenConns: 25
      maxIdleConns: 5
```

## Database Migrations

The chart supports automatic database migrations using dbmate:

### Init Container Mode (Default)

Migrations run in an init container before the main application starts:

```yaml
migration:
  enabled: true
  mode: initContainer  # Runs before pod starts
  resources:
    limits:
      memory: 128Mi
    requests:
      cpu: 50m
      memory: 64Mi
```

### Job Mode (For HA Deployments)

For high availability deployments, run migrations as a pre-install/pre-upgrade Helm hook:

```yaml
migration:
  enabled: true
  mode: job  # Runs as Helm hook before deployment
  job:
    backoffLimit: 3
    ttlSecondsAfterFinished: 300
    resources:
      limits:
        memory: 128Mi
      requests:
        cpu: 50m
        memory: 64Mi
```

### ArgoCD Mode

For ArgoCD deployments:

```yaml
migration:
  enabled: true
  mode: argocd  # Uses ArgoCD PreSync hook
```

## S3 Configuration

### AWS S3

```yaml
config:
  storage:
    type: s3
    s3:
      bucket: my-ncps-cache
      endpoint: https://s3.amazonaws.com
      region: us-east-1
      accessKeyId: YOUR_ACCESS_KEY
      secretAccessKey: YOUR_SECRET_KEY
```

### MinIO

```yaml
config:
  storage:
    type: s3
    s3:
      bucket: ncps
      endpoint: http://minio.minio.svc.cluster.local:9000
      region: us-east-1
      forcePathStyle: true  # Required for MinIO
      accessKeyId: minioadmin
      secretAccessKey: minioadmin
```

## Redis Configuration (High Availability)

For HA deployments with multiple replicas:

```yaml
config:
  redis:
    enabled: true
    addresses:
      - redis-master:6379
      # For Redis Sentinel/Cluster, add multiple addresses
    username: ""  # Optional
    password: ""  # Use existingSecret instead
    existingSecret: ncps-redis-credentials
    db: 0
    useTLS: false
    poolSize: 10

  # Distributed lock configuration
  lock:
    redis:
      keyPrefix: "ncps:lock:"
    downloadTTL: "5m"
    lruTTL: "30m"
    retry:
      maxAttempts: 3
      initialDelay: "100ms"
      maxDelay: "2s"
      jitter: true
    allowDegradedMode: false  # Continue without Redis if connection fails
```

## FSCK (Integrity Check) Settings

Enable and configure the built-in CronJob for periodic integrity checks:

```yaml
fsck:
  enabled: true
  schedule: "0 1 * * *"
  repair: true
  verifiedSince: "168h" # 7 days
  resources:
    limits:
      memory: 6Gi
    requests:
      cpu: 1000m
      memory: 6Gi
  job:
    concurrencyPolicy: Forbid
```

## CDC Configuration (Experimental)

Content-Defined Chunking (CDC) for deduplication:

```yaml
config:
  cdc:
    enabled: true
    # Optional: Tune chunk sizes
    min: 65536
    avg: 262144
    max: 1048576
```

## Upstream Cache Configuration

```yaml
config:
  upstream:
    caches:
      - url: https://cache.nixos.org
        publicKey: cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
      - url: https://nix-community.cachix.org
        publicKey: nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=

    dialerTimeout: "3s"
    responseHeaderTimeout: "3s"

    # Optional: .netrc for authenticated upstreams
    netrcFile: |
      machine cachix.org
      login YOUR_USERNAME
      password YOUR_TOKEN
```

## Observability

### OpenTelemetry

```yaml
config:
  observability:
    opentelemetry:
      enabled: true
      grpcURL: http://otel-collector:4317
```

### Prometheus

```yaml
config:
  observability:
    prometheus:
      enabled: true

serviceMonitor:
  enabled: true
  interval: 30s
  scrapeTimeout: 10s
```

## Resource Management

```yaml
resources:
  limits:
    cpu: 2000m
    memory: 2Gi
  requests:
    cpu: 100m
    memory: 256Mi

# Init container resources (for SQLite directory creation)
initImage:
  resources:
    limits:
      cpu: 100m
      memory: 32Mi
    requests:
      cpu: 10m
      memory: 16Mi

# Migration resources
migration:
  resources:
    limits:
      memory: 128Mi
    requests:
      cpu: 50m
      memory: 64Mi
```

## High Availability Configuration

Complete HA setup with 3 replicas, PostgreSQL, S3, and Redis:

```yaml
mode: deployment
replicaCount: 3

config:
  hostname: cache.example.com

  storage:
    type: s3
    s3:
      existingSecret: ncps-s3-credentials
      bucket: ncps-production
      endpoint: https://s3.amazonaws.com
      region: us-east-1

  database:
    type: postgresql
    postgresql:
      host: postgres-cluster.database.svc.cluster.local
      port: 5432
      database: ncps
      username: ncps
      existingSecret: ncps-db-credentials
      sslMode: require
    pool:
      maxOpenConns: 50
      maxIdleConns: 10

  redis:
    enabled: true
    addresses:
      - redis-master.redis.svc.cluster.local:6379
    existingSecret: ncps-redis-credentials
    poolSize: 10
    lock:
      allowDegradedMode: false

migration:
  enabled: true
  mode: job

podDisruptionBudget:
  enabled: true
  minAvailable: 1

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

## Ingress Configuration

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
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

## Upgrading

### From Single Instance to HA

1. Migrate from SQLite to PostgreSQL/MySQL
1. Switch from local storage to S3
1. Enable Redis
1. Increase replica count

```sh
# Step 1: Backup SQLite database
kubectl exec -n ncps ncps-0 -- /bin/sh -c \
  "sqlite3 /storage/db/ncps.db .dump" > backup.sql

# Step 2: Restore to PostgreSQL
kubectl run psql-import --rm -it --image=postgres:16 -- \
  psql -h postgres -U ncps -d ncps < backup.sql

# Step 3: Upgrade to HA configuration
helm upgrade ncps oci://ghcr.io/kalbasit/helm/ncps \
  --version <chart-version> \
  -n ncps \
  -f ha-values.yaml
```

### Rolling Updates

```yaml
strategy:
  type: RollingUpdate
  rollingUpdate:
    maxSurge: 1
    maxUnavailable: 0
```

## Troubleshooting

### Check Deployment Status

```sh
# Check pod status
kubectl -n ncps get pods

# Check pod logs
kubectl -n ncps logs -l app.kubernetes.io/name=ncps

# Check migration job (if using job mode)
kubectl -n ncps get jobs
kubectl -n ncps logs job/ncps-migration
```

### Common Issues

**Pod fails to start with "database file not found":**

- For SQLite + S3 deployments, ensure `storage.local.persistence.enabled: true` is set
- SQLite requires persistent storage even when using S3 for NAR files

**Migration job fails:**

- Check migration job logs: `kubectl -n ncps logs job/ncps-migration`
- Verify database credentials are correct
- Ensure database is accessible from the cluster

**S3 connection errors:**

- Verify S3 credentials and endpoint
- For MinIO, ensure `config.storage.s3.forcePathStyle: true` is set
- Check endpoint includes proper scheme (http:// or https://)

## Complete Values Reference

See the <a class="reference-link" href="Helm%20Chart/Chart%20Reference.md">Chart Reference</a> for a complete list of all configuration options.

## Next Steps

- <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a>
- <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a>
- <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a>
