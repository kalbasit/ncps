[Home](../../README.md) > [Documentation](../README.md) > [Installation](README.md) > Helm Chart

# Helm Chart Installation

Install ncps on Kubernetes using Helm for simplified configuration and management. This is the recommended method for production Kubernetes deployments.

## Prerequisites

- Kubernetes cluster (version 1.20+)
- Helm 3.0 or later installed
- kubectl configured and connected to your cluster

## Quick Start

### Step 1: Add Helm Repository

```bash
# Add the ncps Helm repository (when published)
# helm repo add ncps https://kalbasit.github.io/ncps/
# helm repo update

# For now, use the chart from the repository
git clone https://github.com/kalbasit/ncps.git
cd ncps
```

### Step 2: Install with Default Values

```bash
# Create namespace
kubectl create namespace ncps

# Install the chart
helm install ncps ./charts/ncps \
  --namespace ncps \
  --set cache.hostName=cache.example.com
```

This installs ncps with:
- Single replica
- Local storage (PersistentVolumeClaim)
- SQLite database
- Default upstream caches

### Step 3: Verify Installation

```bash
# Check pod status
kubectl -n ncps get pods

# Check service
kubectl -n ncps get svc

# Get public key
kubectl -n ncps run test --rm -it --image=curlimages/curl -- \
  curl http://ncps:8501/pubkey
```

## Configuration Options

### Basic Configuration

Create a `values.yaml` file:

```yaml
cache:
  hostName: cache.example.com

  upstream:
    urls:
      - https://cache.nixos.org
      - https://nix-community.cachix.org
    publicKeys:
      - cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
      - nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs=

persistence:
  enabled: true
  size: 50Gi
```

Install with custom values:
```bash
helm install ncps ./charts/ncps -n ncps -f values.yaml
```

### S3 Storage Configuration

For production deployments with S3:

```yaml
cache:
  hostName: cache.example.com

  storage:
    s3:
      enabled: true
      bucket: my-ncps-cache
      endpoint: s3.amazonaws.com
      region: us-east-1
      credentials:
        accessKeyId: YOUR_ACCESS_KEY
        secretAccessKey: YOUR_SECRET_KEY

  database:
    url: postgresql://user:pass@postgres:5432/ncps?sslmode=require

persistence:
  enabled: false  # Not needed with S3
```

### High Availability Configuration

Deploy with 3 replicas, Redis, and S3:

```yaml
replicaCount: 3

cache:
  hostName: cache.example.com

  storage:
    s3:
      enabled: true
      bucket: ncps-cache
      endpoint: s3.amazonaws.com
      region: us-east-1

  database:
    url: postgresql://ncps:password@postgres:5432/ncps

  redis:
    enabled: true
    addrs:
      - redis:6379

podDisruptionBudget:
  enabled: true
  minAvailable: 2

ingress:
  enabled: true
  className: nginx
  hosts:
    - host: cache.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: cache-tls
      hosts:
        - cache.example.com
```

Install:
```bash
helm install ncps ./charts/ncps -n ncps -f values-ha.yaml
```

### With Monitoring

Enable Prometheus metrics and ServiceMonitor:

```yaml
prometheus:
  enabled: true
  serviceMonitor:
    enabled: true
    interval: 30s
```

## Helm Commands

### Install/Upgrade

```bash
# Install
helm install ncps ./charts/ncps -n ncps -f values.yaml

# Upgrade
helm upgrade ncps ./charts/ncps -n ncps -f values.yaml

# Install or upgrade
helm upgrade --install ncps ./charts/ncps -n ncps -f values.yaml
```

### View Status

```bash
# Check release status
helm status ncps -n ncps

# List all releases
helm list -n ncps

# Get values
helm get values ncps -n ncps
```

### Uninstall

```bash
# Uninstall release
helm uninstall ncps -n ncps

# Uninstall and delete PVCs
helm uninstall ncps -n ncps
kubectl -n ncps delete pvc --all
```

## Common Configurations

### Using Existing Secret for S3 Credentials

Create a secret:
```bash
kubectl -n ncps create secret generic ncps-s3-credentials \
  --from-literal=access-key-id=YOUR_ACCESS_KEY \
  --from-literal=secret-access-key=YOUR_SECRET_KEY
```

Reference in values:
```yaml
cache:
  storage:
    s3:
      enabled: true
      bucket: ncps-cache
      credentials:
        existingSecret: ncps-s3-credentials
```

### Using External PostgreSQL

```yaml
cache:
  database:
    url: postgresql://ncps:password@external-postgres.example.com:5432/ncps?sslmode=require

postgresql:
  enabled: false  # Don't deploy PostgreSQL subchart
```

### Custom Resource Limits

```yaml
resources:
  requests:
    memory: "512Mi"
    cpu: "250m"
  limits:
    memory: "2Gi"
    cpu: "2000m"
```

### Affinity and Node Selection

```yaml
affinity:
  podAntiAffinity:
    preferredDuringSchedulingIgnoredDuringExecution:
      - weight: 100
        podAffinityTerm:
          labelSelector:
            matchLabels:
              app.kubernetes.io/name: ncps
          topologyKey: kubernetes.io/hostname

nodeSelector:
  disktype: ssd
```

## Complete Values Reference

For all available configuration options, see:
- **[Helm Chart Documentation](/charts/ncps/README.md)** - Comprehensive guide
- **[values.yaml](/charts/ncps/values.yaml)** - Default values with comments
- **[values.schema.json](/charts/ncps/values.schema.json)** - JSON schema for validation

## Troubleshooting

### Chart Installation Fails

```bash
# Lint the chart
helm lint ./charts/ncps -f values.yaml

# Dry-run to see generated manifests
helm install ncps ./charts/ncps -n ncps -f values.yaml --dry-run --debug

# Check for validation errors
helm install ncps ./charts/ncps -n ncps -f values.yaml --debug
```

### Pods Not Starting

```bash
# Check pod status
kubectl -n ncps get pods

# Check events
kubectl -n ncps get events --sort-by='.lastTimestamp'

# View pod logs
kubectl -n ncps logs -l app.kubernetes.io/name=ncps

# Describe pod
kubectl -n ncps describe pod <pod-name>
```

### Values Not Applied

```bash
# Check current values
helm get values ncps -n ncps

# Check all values (including defaults)
helm get values ncps -n ncps --all

# Verify manifest
helm get manifest ncps -n ncps
```

See the [Troubleshooting Guide](../operations/troubleshooting.md) for more help.

## Upgrading ncps

### Upgrade to New Version

```bash
# Update repository
git pull origin main  # Or helm repo update when published

# Upgrade release
helm upgrade ncps ./charts/ncps -n ncps -f values.yaml

# Check rollout status
kubectl -n ncps rollout status deployment/ncps
```

### Rolling Back

```bash
# View revision history
helm history ncps -n ncps

# Rollback to previous version
helm rollback ncps -n ncps

# Rollback to specific revision
helm rollback ncps 2 -n ncps
```

## Next Steps

1. **[Configure Clients](../usage/client-setup.md)** - Set up Nix clients to use your cache
2. **[Configure Monitoring](../operations/monitoring.md)** - Set up Prometheus and Grafana
3. **[Review Helm Chart Docs](/charts/ncps/README.md)** - Comprehensive chart reference
4. **[Plan for HA](../deployment/high-availability.md)** - High availability setup

## Related Documentation

- **[Helm Chart README](/charts/ncps/README.md)** - Complete chart documentation
- [Kubernetes Installation](kubernetes.md) - Manual Kubernetes deployment
- [High Availability Guide](../deployment/high-availability.md) - HA deployment
- [Configuration Reference](../configuration/reference.md) - All configuration options
- [Monitoring Guide](../operations/monitoring.md) - Observability setup
