[Home](../../README.md) > [Documentation](../README.md) > Usage

# Usage Guide

Learn how to use ncps effectively.

## Guides

- **[Client Setup](client-setup.md)** - Configure Nix clients to use your cache
- **[Cache Management](cache-management.md)** - Manage cache size and cleanup

## Quick Links

### Setting Up Clients

1. [Get your public key](client-setup.md#get-public-key)
2. [Configure NixOS clients](client-setup.md#nixos-configuration)
3. [Configure non-NixOS clients](client-setup.md#non-nixos-configuration)
4. [Verify it works](client-setup.md#verify)

### Managing Your Cache

1. [Configure cache size limits](cache-management.md#size-limits)
2. [Set up LRU cleanup](cache-management.md#lru-cleanup)
3. [Monitor cache usage](cache-management.md#monitoring)

## Common Tasks

**Configure a new Nix client:**
See [Client Setup Guide](client-setup.md)

**Check cache is working:**
```bash
nix-build --option substituters "http://your-ncps:8501" ...
# Check ncps logs for cache hits
```

**Clean up old cached packages:**
See [Cache Management Guide](cache-management.md#manual-cleanup)

## Related Documentation

- [Configuration Reference](../configuration/reference.md) - Configuration options
- [Monitoring Guide](../operations/monitoring.md) - Monitor cache performance
- [Troubleshooting Guide](../operations/troubleshooting.md) - Solve common issues
