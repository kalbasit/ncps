# NixOS

## NixOS Installation

Install ncps on NixOS using the built-in service module for native integration and simplified management.

## Prerequisites

- NixOS 25.05 or later (earlier versions don't include the ncps module)
- Sufficient disk space for cache storage
- Basic familiarity with NixOS configuration

## Quick Start

### Basic Configuration

Add to your `configuration.nix`:

```
{
  services.ncps = {
    enable = true;
    cache.hostName = "your-ncps-hostname";
    upstream = {
      caches = [
        "https://cache.nixos.org"
        "https://nix-community.cachix.org"
      ];
      publicKeys = [
        "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
        "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
      ];
    };
  };
}
```

Rebuild your system:

```
sudo nixos-rebuild switch
```

The service will:

- Create the ncps user and group automatically
- Set up directories with correct permissions
- Initialize the database
- Start the ncps service

### Verify Installation

```
# Check service status
systemctl status ncps

# View logs
journalctl -u ncps -f

# Test the cache
curl http://localhost:8501/nix-cache-info
curl http://localhost:8501/pubkey
```

## Advanced Configuration

### Custom Storage Location

```
{
  services.ncps = {
    enable = true;
    cache = {
      hostName = "cache.example.com";
      dataPath = "/mnt/large-disk/ncps";
      tempPath = "/mnt/fast-disk/ncps-temp";  # Available in NixOS 25.09+
      maxSize = "100G";
    };
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
  };
}
```

### LRU Cache Cleanup Schedule

```
{
  services.ncps = {
    enable = true;
    cache = {
      hostName = "cache.example.com";
      maxSize = "50G";
      lru.schedule = "0 2 * * *";  # Daily at 2 AM
    };
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
  };
}
```

### Allow PUT/DELETE Operations

```
{
  services.ncps = {
    enable = true;
    cache = {
      hostName = "cache.example.com";
      allowPutVerb = true;     # Allow uploads
      allowDeleteVerb = true;  # Allow deletions
    };
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
  };
}
```

### Custom Listen Address

```
{
  services.ncps = {
    enable = true;
    cache.hostName = "cache.example.com";
    server.addr = "0.0.0.0:8501";  # Listen on all interfaces
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
  };

  # Open firewall
  networking.firewall.allowedTCPPorts = [ 8501 ];
}
```

### With PostgreSQL Database

```
{
  services.ncps = {
    enable = true;
    cache = {
      hostName = "cache.example.com";
      databaseURL = "postgresql://ncps:password@localhost:5432/ncps?sslmode=require";
    };
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
  };

  # Set up PostgreSQL
  services.postgresql = {
    enable = true;
    ensureDatabases = [ "ncps" ];
    ensureUsers = [{
      name = "ncps";
      ensureDBOwnership = true;
    }];
  };
}
```

## S3 Storage Support

**Note:** S3 storage configuration in the NixOS module will be available in a future release. For now, use the Docker or Helm installation methods for S3 storage.

To use S3 storage with NixOS today:

- Install ncps via Docker/Podman on NixOS
- Use the <a class="reference-link" href="Docker.md">Docker</a>

## Observability Configuration

### Enable Prometheus Metrics

```
{
  services.ncps = {
    enable = true;
    cache.hostName = "cache.example.com";
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
  };

  # Enable Prometheus scraping
  services.prometheus = {
    enable = true;
    scrapeConfigs = [{
      job_name = "ncps";
      static_configs = [{
        targets = [ "localhost:8501" ];
      }];
    }];
  };
}
```

### Enable OpenTelemetry

```
{
  services.ncps = {
    enable = true;
    cache.hostName = "cache.example.com";
    upstream = {
      caches = [ "https://cache.nixos.org" ];
      publicKeys = [ "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=" ];
    };
    # OpenTelemetry configuration via environment or config file
  };

  # Set environment variables for the service
  systemd.services.ncps.environment = {
    OTEL_ENABLED = "true";
    OTEL_GRPC_URL = "http://localhost:4317";
  };
}
```

## Configuration Options Reference

For a complete list of all available options, search the NixOS options:

**Online:** [NixOS Options Search](https://search.nixos.org/options?query=services.ncps)

**From command line:**

```
nixos-option services.ncps
```

**Common options:**

| Option | Description | Type | Default |
| --- | --- | --- | --- |
| `services.ncps.enable` | Enable ncps service | boolean | false |
| `services.ncps.cache.hostName` | Cache hostname for key generation | string | (required) |
| `services.ncps.cache.dataPath` | Data storage directory | path | /var/lib/ncps |
| `services.ncps.cache.tempPath` | Temporary download directory | path | (system temp) |
| `services.ncps.cache.databaseURL` | Database connection URL | string | sqlite:${dataPath}/db/db.sqlite |
| `services.ncps.cache.maxSize` | Maximum cache size | string | (unlimited) |
| `services.ncps.cache.lru.schedule` | LRU cleanup cron schedule | string | (disabled) |
| `services.ncps.cache.allowPutVerb` | Allow PUT uploads | boolean | false |
| `services.ncps.cache.allowDeleteVerb` | Allow DELETE operations | boolean | false |
| `services.ncps.server.addr` | Listen address | string | :8501 |
| `services.ncps.upstream.caches` | Upstream cache URLs | list of strings | (required) |
| `services.ncps.upstream.publicKeys` | Upstream public keys | list of strings | (required) |

## Service Management

### Control the Service

```
# Start
sudo systemctl start ncps

# Stop
sudo systemctl stop ncps

# Restart
sudo systemctl restart ncps

# Check status
sudo systemctl status ncps

# Enable (auto-start on boot)
sudo systemctl enable ncps

# Disable
sudo systemctl disable ncps
```

### View Logs

```
# View recent logs
journalctl -u ncps

# Follow logs (live)
journalctl -u ncps -f

# View logs since boot
journalctl -u ncps -b

# View last 100 lines
journalctl -u ncps -n 100
```

## Troubleshooting

### Service Won't Start

```
# Check service status
systemctl status ncps

# View full logs
journalctl -u ncps -n 50 --no-pager

# Check configuration
nixos-rebuild dry-build
```

### Permission Errors

The NixOS module automatically handles permissions, but if you're using a custom `dataPath`:

```
# Check ownership
ls -la /path/to/dataPath

# Service runs as 'ncps' user
# Ensure the directory is owned by ncps:ncps
```

### Database Issues

```
# Check database path
systemctl cat ncps | grep database

# Manually run migration (if needed)
sudo -u ncps dbmate --url="sqlite:/var/lib/ncps/db/db.sqlite" migrate up
```

### Port Already in Use

```
# Find what's using port 8501
sudo lsof -i :8501

# Change the listen address in configuration
```

See the <a class="reference-link" href="../Operations/Troubleshooting.md">Troubleshooting</a> for more help.

## Upgrading

### Upgrade ncps

ncps is updated with your NixOS system:

```
# Update channel
sudo nix-channel --update

# Rebuild system
sudo nixos-rebuild switch
```

### Upgrade to Specific Version

```
{
  # Pin to specific version (if needed)
  nixpkgs.config.packageOverrides = pkgs: {
    ncps = pkgs.ncps.overrideAttrs (old: {
      version = "0.5.0";
      src = pkgs.fetchFromGitHub {
        owner = "kalbasit";
        repo = "ncps";
        rev = "v0.5.0";
        hash = "...";
      };
    });
  };
}
```

## Next Steps

1. <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Set up your Nix clients to use the cache
1. <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - Explore more configuration options
1. <a class="reference-link" href="../Operations/Monitoring.md">Monitoring</a> - Configure observability
1. <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a> - Consider high availability (use Docker/K8s)

## Related Documentation

- <a class="reference-link" href="https://search.nixos.org/options?query=services.ncps">NixOS Options Search</a> - All module options
- <a class="reference-link" href="Docker.md">Docker</a> - For S3 storage on NixOS
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a> - All configuration options
- <a class="reference-link" href="../Usage/Client%20Setup.md">Client Setup</a> - Configure Nix clients
