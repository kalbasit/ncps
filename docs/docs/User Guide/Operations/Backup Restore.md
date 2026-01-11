# Backup Restore

## Backup & Restore Guide

Backup strategies and recovery procedures.

## Database Backups

### SQLite

**Backup:**

```
# Stop ncps (recommended)
systemctl stop ncps

# Copy database
cp /var/lib/ncps/db/db.sqlite /backup/db.sqlite.$(date +%Y%m%d)

# Restart ncps
systemctl start ncps
```

**Restore:**

```
systemctl stop ncps
cp /backup/db.sqlite.20240101 /var/lib/ncps/db/db.sqlite
systemctl start ncps
```

### PostgreSQL

**Backup:**

```
pg_dump -h localhost -U ncps ncps > /backup/ncps.sql
```

**Restore:**

```
psql -h localhost -U ncps ncps < /backup/ncps.sql
```

### MySQL

**Backup:**

```
mysqldump -u ncps -p ncps > /backup/ncps.sql
```

**Restore:**

```
mysql -u ncps -p ncps < /backup/ncps.sql
```

## Storage Backups

### Local Storage

**Backup:**

```
tar -czf /backup/ncps-storage.tar.gz /var/lib/ncps/
```

**Restore:**

```
tar -xzf /backup/ncps-storage.tar.gz -C /
```

### S3 Storage

S3 has built-in durability. Enable versioning for protection:

```
aws s3api put-bucket-versioning \
  --bucket ncps-cache \
  --versioning-configuration Status=Enabled
```

## Backup Strategies

### Development

- Database: Daily backups
- Storage: Optional (can rebuild)

### Production Single-Instance

- Database: Daily automated backups
- Storage: Weekly backups or S3 with versioning

### Production HA

- Database: Automated backups with replication
- Storage: S3 with versioning (built-in)
- Redis: Optional (ephemeral locks)

## Disaster Recovery

1. Stop ncps instances
1. Restore database from backup
1. Restore storage from backup (if local)
1. Start ncps instances
1. Verify functionality

## Related Documentation

- <a class="reference-link" href="../Configuration/Database.md">Database</a> - Database setup
- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Storage setup
