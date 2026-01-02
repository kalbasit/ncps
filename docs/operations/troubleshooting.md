[Home](../../README.md) > [Documentation](../README.md) > [Operations](README.md) > Troubleshooting

# Troubleshooting Guide

Common issues and solutions.

## Service Issues

### Service Won't Start

**Check logs:**

```bash
# Docker
docker logs ncps

# Systemd
journalctl -u ncps -f
```

**Common causes:**

- Missing required flags
- Database not initialized
- Permission errors
- Port already in use

### Can't Access Cache

**Test connectivity:**

```bash
curl http://your-ncps:8501/nix-cache-info
```

**Check:**

- Service is running
- Port 8501 is open
- Firewall rules
- Network connectivity

## Database Issues

### Database Locked (SQLite)

SQLite only allows one writer:

- Check no other processes are accessing the database
- Restart ncps
- Use PostgreSQL/MySQL for concurrent access

### Migration Errors

```bash
# Run migrations manually
dbmate --url=<database-url> migrate up
```

## Storage Issues

### Disk Full

```bash
# Check disk space
df -h

# Enable LRU cleanup
--cache-max-size=100G
--cache-lru-schedule="0 2 * * *"
```

### Permission Errors

```bash
# Fix ownership
sudo chown -R ncps:ncps /var/lib/ncps
```

## HA-Specific Issues

### Download Locks Not Working

**Check:**

- Redis is configured and reachable
- All instances use same Redis
- Check logs for lock messages

### High Lock Contention

**Solutions:**

- Increase retry settings
- Increase lock TTLs
- Scale down instances if too many

See [Distributed Locking Guide](../deployment/distributed-locking.md#troubleshooting).

## Debug Logging

Enable debug mode:

```bash
ncps serve --log-level=debug
```

## Related Documentation

- [Monitoring Guide](monitoring.md) - Set up monitoring
- [Operations Guide](README.md) - All operational guides
