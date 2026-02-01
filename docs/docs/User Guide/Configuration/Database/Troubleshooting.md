# Troubleshooting
### SQLite Issues

**Database Locked:**

*   Only one writer at a time
*   Ensure no other processes are accessing the database
*   Check for stale lock files

**Corruption:**

```sh
# Check integrity
sqlite3 /var/lib/ncps/db/db.sqlite "PRAGMA integrity_check;"

# Recover if possible
sqlite3 /var/lib/ncps/db/db.sqlite ".recover" | sqlite3 recovered.db
```

### PostgreSQL Issues

**Connection Refused:**

*   Check PostgreSQL is running: `systemctl status postgresql`
*   Verify `pg_hba.conf` allows connections from ncps host
*   Check firewall rules

**Authentication Failed:**

*   Verify username and password
*   Check `pg_hba.conf` authentication method
*   Ensure user has correct privileges

**Too Many Connections:**

*   Reduce pool size in ncps
*   Increase `max_connections` in PostgreSQL
*   Check for connection leaks

### MySQL Issues

**Connection Refused:**

*   Check MySQL is running: `systemctl status mysql`
*   Verify `bind-address` in my.cnf
*   Check firewall rules

**Access Denied:**

*   Verify username, password, and host in GRANT
*   Check user privileges: `SHOW GRANTS FOR 'ncps'@'%';`
*   Flush privileges after changes

See the [Troubleshooting Guide](../../Operations/Troubleshooting.md) for more help.