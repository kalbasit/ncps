# Troubleshooting

## Troubleshooting Guide

Common issues and solutions.

## Service Issues

### Service Won't Start

**Check logs:**

```
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

```
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

```
# Run migrations manually
dbmate --url=<database-url> migrate up
```

## Storage Issues

### Disk Full

```
# Check disk space
df -h

# Enable LRU cleanup
--cache-max-size=100G
--cache-lru-schedule="0 2 * * *"
```

### Permission Errors

```
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

See <a class="reference-link" href="../Deployment/Distributed%20Locking.md">Distributed Locking</a>.

## Debug Logging

Enable debug mode:

```
ncps serve --log-level=debug
```

## Related Documentation

- <a class="reference-link" href="Monitoring.md">Monitoring</a> - Set up monitoring
- <a class="reference-link" href="../Operations.md">Operations</a> - All operational guides
