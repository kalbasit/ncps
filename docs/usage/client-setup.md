[Home](../../README.md) > [Documentation](../README.md) > [Usage](README.md) > Client Setup

# Client Setup Guide

Configure your Nix clients to use your ncps cache.

## Get Public Key

First, retrieve the public key from your ncps instance:

```bash
curl http://your-ncps-hostname:8501/pubkey
```

**Example output:**

```
your-ncps-hostname:abc123def456...=
```

Save this public key - you'll need it for client configuration.

## NixOS Configuration

Add ncps to your `/etc/nixos/configuration.nix`:

```nix
{
  nix.settings = {
    substituters = [
      "http://your-ncps-hostname:8501"  # Add your ncps cache
      "https://cache.nixos.org"          # Keep official cache as fallback
    ];

    trusted-public-keys = [
      "your-ncps-hostname:abc123def456...="  # Paste public key here
      "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
    ];
  };
}
```

**Then rebuild:**

```bash
sudo nixos-rebuild switch
```

### Behind HTTPS Reverse Proxy

If ncps is behind an HTTPS reverse proxy:

```nix
{
  nix.settings = {
    substituters = [
      "https://cache.example.com"  # Use HTTPS
      "https://cache.nixos.org"
    ];

    trusted-public-keys = [
      "cache.example.com:abc123def456...="
      "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
    ];
  };
}
```

## Non-NixOS Configuration

Edit your Nix configuration file (typically `/etc/nix/nix.conf` or `~/.config/nix/nix.conf`):

```ini
substituters = http://your-ncps-hostname:8501 https://cache.nixos.org
trusted-public-keys = your-ncps-hostname:abc123def456...= cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY=
```

**Then restart Nix daemon:**

```bash
# On systemd systems
sudo systemctl restart nix-daemon

# On macOS
sudo launchctl stop org.nixos.nix-daemon
sudo launchctl start org.nixos.nix-daemon
```

## Verify Configuration

### Check Configuration

```bash
nix show-config | grep substituters
nix show-config | grep trusted-public-keys
```

Verify your ncps cache appears in the output.

### Test Cache Connectivity

```bash
curl http://your-ncps-hostname:8501/nix-cache-info
```

Should return cache information.

### Test Package Download

```bash
# Try building something
nix-build '<nixpkgs>' -A hello

# Check ncps logs for cache hits/misses
# Docker: docker logs ncps
# Systemd: journalctl -u ncps -f
```

Look for log messages like:

- `serving nar from cache` - Cache hit!
- `downloading nar from upstream` - Cache miss, fetching from upstream

## Priority and Order

Nix tries substituters in order. Place your ncps cache first for best performance:

```nix
{
  nix.settings.substituters = [
    "http://your-ncps:8501"        # Try ncps first
    "https://cache.nixos.org"       # Fallback to official cache
  ];
}
```

## Multiple ncps Caches

You can configure multiple ncps caches:

```nix
{
  nix.settings = {
    substituters = [
      "http://ncps-local:8501"     # Local cache (fastest)
      "http://ncps-remote:8501"    # Remote cache
      "https://cache.nixos.org"     # Official cache (fallback)
    ];

    trusted-public-keys = [
      "ncps-local:key1="
      "ncps-remote:key2="
      "cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="
    ];
  };
}
```

## Trusted Users

For multi-user Nix installations, you may need to configure trusted users:

```nix
{
  nix.settings.trusted-users = [ "root" "youruser" ];
}
```

Or in `/etc/nix/nix.conf`:

```ini
trusted-users = root youruser
```

## Per-Project Configuration

Override cache settings for specific projects using `--option`:

```bash
nix-build \
  --option substituters "http://project-cache:8501 https://cache.nixos.org" \
  --option trusted-public-keys "project-cache:key= cache.nixos.org-1:key=" \
  ...
```

## Troubleshooting

### Cache Not Being Used

**Check configuration:**

```bash
nix show-config | grep substituters
```

**Check ncps is reachable:**

```bash
curl http://your-ncps:8501/nix-cache-info
```

**Check public key is trusted:**

```bash
nix show-config | grep trusted-public-keys
```

### Permission Denied

Add your user to trusted users:

```nix
nix.settings.trusted-users = [ "root" "youruser" ];
```

### Still Downloading from Official Cache

**Possible causes:**

1. ncps doesn't have the package cached yet (first download)
1. ncps cache listed after official cache (order matters)
1. Public key not trusted

**Check ncps logs** to see if requests are reaching it.

See the [Troubleshooting Guide](../operations/troubleshooting.md) for more help.

## Next Steps

1. **[Monitor Cache Performance](../operations/monitoring.md)** - Set up monitoring
1. **[Manage Cache Size](cache-management.md)** - Configure LRU cleanup
1. **[Review Configuration](../configuration/reference.md)** - Explore more options

## Related Documentation

- [Configuration Reference](../configuration/reference.md) - All configuration options
- [Monitoring Guide](../operations/monitoring.md) - Monitor cache hits/misses
- [Troubleshooting Guide](../operations/troubleshooting.md) - Solve issues
