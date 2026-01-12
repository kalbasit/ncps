# Usage

## Usage Guide

Learn how to use ncps effectively.

## Guides

- <a class="reference-link" href="Usage/Client%20Setup.md">Client Setup</a> - Configure Nix clients to use your cache
- <a class="reference-link" href="Usage/Cache%20Management.md">Cache Management</a> - Manage cache size and cleanup

## Quick Links

### Setting Up Clients

1. [NixOS Configuration](Usage/Client%20Setup.md)
1. [Non-NixOS Linux](Usage/Client%20Setup.md)
1. [macOS Setup](Usage/Client%20Setup.md)
1. [CI/CD Integration](Usage/Client%20Setup.md)

### Managing Your Cache

1. [Automatic GC](Usage/Cache%20Management.md)
1. [Manual Cleanup](Usage/Cache%20Management.md)
1. [Size Monitoring](Usage/Cache%20Management.md)

## Common Tasks

**Configure a new Nix client:** See [Client Setup Guide](Usage/Client%20Setup.md)

**Check cache is working:**

```
nix-build --option substituters "http://your-ncps:8501" ...
# Check ncps logs for cache hits
```

**Clean up old cached packages:** See <a class="reference-link" href="Usage/Cache%20Management.md">Cache Management</a>

## Related Documentation

- <a class="reference-link" href="Configuration/Reference.md">Configuration Reference</a> - Configuration options
- [Monitoring Guide](Operations/Monitoring.md) - Monitor cache performance
- [Troubleshooting Guide](Operations/Troubleshooting.md) - Solve common issues
