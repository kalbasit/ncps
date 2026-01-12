# Upgrading

## Upgrading Guide

Upgrade ncps to newer versions.

## Single-Instance Upgrade

### Docker

```
# Stop current instance
docker stop ncps

# Pull new version
docker pull ghcr.io/kalbasit/ncps:latest

# Remove old container
docker rm ncps

# Start with new version (same command as before)
docker run -d --name ncps ...
```

### NixOS

```
# Update channel
sudo nix-channel --update

# Rebuild system
sudo nixos-rebuild switch
```

### Kubernetes/Helm

```
# Update chart
helm upgrade ncps ./charts/ncps -n ncps -f values.yaml
```

## High Availability Upgrade

Perform rolling updates:

```
# Update instance 1
docker stop ncps-1
docker rm ncps-1
docker pull ghcr.io/kalbasit/ncps:latest
docker run -d --name ncps-1 ...

# Wait and verify instance 1 is healthy

# Update instance 2
# ... repeat process

# Update instance 3
# ... repeat process
```

**Kubernetes automatic rolling update:**

```yaml
spec:
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 1
```

## Database Migrations

Database migrations run automatically on startup. Ensure:

1. Backup database before upgrading
1. migrations complete successfully (check logs)
1. All instances use same database schema version

## Breaking Changes

Check release notes for breaking changes before upgrading.

## Rollback

If upgrade fails:

**Docker:**

```
docker stop ncps
docker rm ncps
docker run -d --name ncps ghcr.io/kalbasit/ncps:v0.4.0 ...  # Previous version
```

**Helm:**

```
helm rollback ncps -n ncps
```

## Related Documentation

- <a class="reference-link" href="../Installation.md">Installation</a> - Installation methods
- <a class="reference-link" href="../Deployment.md">Deployment</a> - Deployment strategies
