# Integrity Check (fsck)

## Overview

The `ncps fsck` command checks for consistency issues between the database and storage backend. Over time, database records and physical storage files can drift apart due to server crashes, manual file deletions, failed migrations, or other unexpected events. Running `fsck` detects and optionally repairs these inconsistencies.

## What fsck Checks

### Standard Checks (always performed)

| Issue | Description |
| --- | --- |
| **Narinfos without nar_files** | Narinfo records in the database that have no linked `nar_file` entry |
| **Orphaned nar_files (DB only)** | `nar_file` records in the database not linked to any narinfo |
| **Nar_files missing from storage** | `nar_file` records in the database whose physical file is absent from storage |
| **Orphaned NAR files in storage** | NAR files on disk or in S3 that have no corresponding database record |

### CDC Checks (when CDC is enabled)

| Issue | Description |
| --- | --- |
| **Orphaned chunks (DB only)** | Chunk records in the database not linked to any `nar_file` |
| **Chunks missing from storage** | Chunk records in the database whose physical chunk file is absent |
| **Orphaned chunk files** | Chunk files in storage that have no corresponding database record |

## How fsck Works

fsck runs in three phases:

1. **Phase 1 – Collect suspects**: Runs database queries and walks storage to identify potential issues.
1. **Phase 2 – Re-verify**: Each suspected issue is individually re-checked to filter out items that were in-flight (being added or removed concurrently). This prevents false positives from in-progress cache operations.
1. **Phase 3 – Repair** (optional): Re-verifies each item one final time before deleting, then removes all confirmed issues.

The double re-verify design means fsck is safe to run against a live cache without taking it offline.

## Usage

> [!WARNING]
> The **fsck** command can be expensive to run, especially when using S3 storage because it walks the storage to find orphaned NARs or Chunks.

### Check Only (Report Mode)

Run fsck without any flags to detect issues and print a summary. If issues are found, you will be prompted to confirm repair:

```sh
ncps fsck \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps"
```

Example output when issues are found:

```
ncps fsck summary
=================
Narinfos without nar_files:         0
Orphaned nar_files (DB only):       0
Nar_files missing from storage:     1
Orphaned NAR files in storage:      5
-----------------
Total issues:                       6

Repair all issues? [y/N]:
```

Example output when everything is consistent:

```
ncps fsck summary
=================
Narinfos without nar_files:         0
Orphaned nar_files (DB only):       0
Nar_files missing from storage:     0
Orphaned NAR files in storage:      0
-----------------
Total issues:                       0
All checks passed.
```

### Automatic Repair

Use `--repair` to fix all issues without a prompt:

```sh
ncps fsck \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps" \
  --repair
```

### Dry Run

Use `--dry-run` to print the summary of issues without making any changes:

```sh
ncps fsck \
  --cache-database-url="sqlite:/var/lib/ncps/db.sqlite" \
  --cache-storage-local="/var/lib/ncps" \
  --dry-run
```

When issues are found in dry-run mode, the command exits with a non-zero status so it can be used in scripts.

### S3 Storage

```sh
ncps fsck \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-s3-bucket="ncps-cache" \
  --cache-storage-s3-endpoint="https://s3.amazonaws.com" \
  --cache-storage-s3-region="us-east-1" \
  --cache-storage-s3-access-key-id="..." \
  --cache-storage-s3-secret-access-key="..."
```

### PostgreSQL or MySQL

```sh
ncps fsck \
  --cache-database-url="postgresql://user:pass@localhost/ncps" \
  --cache-storage-local="/var/lib/ncps"
```

## Flags Reference

### Core Flags

| Flag | Description |
| --- | --- |
| `--repair` | Automatically fix all detected issues |
| `--dry-run` | Show what would be fixed without making any changes |

### Storage Flags

| Flag | Description |
| --- | --- |
| `--cache-storage-local` | Path to local cache storage directory |
| `--cache-storage-s3-bucket` | S3 bucket name |
| `--cache-storage-s3-endpoint` | S3-compatible endpoint URL |
| `--cache-storage-s3-region` | S3 region (optional) |
| `--cache-storage-s3-access-key-id` | S3 access key ID |
| `--cache-storage-s3-secret-access-key` | S3 secret access key |
| `--cache-storage-s3-force-path-style` | Force path-style S3 addressing |

### Database Flags

| Flag | Default | Description |
| --- | --- | --- |
| `--cache-database-url` | _(required)_ | Database URL (`sqlite:`, `postgresql://`, or `mysql://`) |
| `--cache-database-pool-max-open-conns` | — | Maximum open database connections |
| `--cache-database-pool-max-idle-conns` | — | Maximum idle database connections |

### Distributed Locking Flags (optional)

These flags are optional and only needed if you want fsck to use distributed (Redis-backed) locks during the check. For standalone use, local locking is used by default.

| Flag | Default | Description |
| --- | --- | --- |
| `--cache-redis-addrs` | — | Redis server addresses (enables distributed locking) |
| `--cache-redis-username` | — | Redis username |
| `--cache-redis-password` | — | Redis password |
| `--cache-redis-db` | `0` | Redis database number |
| `--cache-redis-use-tls` | `false` | Use TLS for Redis connections |
| `--cache-redis-pool-size` | `10` | Redis connection pool size |
| `--cache-lock-backend` | `local` | Lock backend: `local` or `redis` |
| `--cache-lock-allow-degraded-mode` | `false` | Fall back to local locks if Redis is unavailable |

## Repair Behaviour

When `--repair` is used (or confirmed interactively), fsck deletes the following:

| Issue | Action |
| --- | --- |
| Narinfos without nar_files | Delete the narinfo DB record (and its references/signatures via cascade) |
| Orphaned nar_files (DB only) | Delete the `nar_file` DB record |
| Nar_files missing from storage | Delete the `nar_file` DB record; also deletes any narinfo that becomes orphaned as a result (cascade cleanup — no second run needed) |
| Orphaned NAR files in storage | Delete the physical file from storage |
| [CDC] Orphaned chunks (DB only) | Delete the chunk DB record |
| [CDC] Chunks missing from storage | Delete the chunk DB record |
| [CDC] Orphaned chunk files | Delete the physical chunk file from storage |

> **Note:** Repair does not recover missing data — it removes the inconsistent records. If you need to recover missing NAR files, restore from a backup before running repair.

## Exit Codes

| Exit Code | Meaning |
| --- | --- |
| `0` | All checks passed (or repair completed successfully) |
| Non-zero | Issues were found and either `--dry-run` was used, the prompt was answered with `N`, or an error occurred |

## Scheduling Regular Checks

For production deployments it is good practice to run `fsck` periodically as a health check:

```sh
# Daily check — alert if issues found (no repair)
ncps fsck \
  --cache-database-url="$CACHE_DATABASE_URL" \
  --cache-storage-local="$CACHE_STORAGE_LOCAL" \
  --dry-run

# Weekly automated repair
ncps fsck \
  --cache-database-url="$CACHE_DATABASE_URL" \
  --cache-storage-local="$CACHE_STORAGE_LOCAL" \
  --repair
```

As a systemd timer:

```
[Unit]
Description=ncps integrity check

[Service]
Type=oneshot
ExecStart=ncps fsck \
  --cache-database-url=sqlite:/var/lib/ncps/db.sqlite \
  --cache-storage-local=/var/lib/ncps \
  --dry-run

[Install]
WantedBy=timers.target
```

## Troubleshooting

### Large number of orphaned NAR files in storage

**Possible causes:**

- The server was stopped mid-write (NAR file written, DB record not yet committed)
- Manual file operations on the storage directory
- A failed migration left files behind

**Action:** Run with `--repair` to remove the orphaned files. The re-verify phase guards against removing in-flight writes.

### Nar_files missing from storage

**Possible causes:**

- Files were deleted manually from the storage directory or S3 bucket
- A storage backend failure caused partial data loss

**Action:** If backups are available, restore the missing files before running repair. Otherwise, run with `--repair` to remove the orphaned DB records. Any narinfos that become orphaned as a result are also cleaned up automatically in the same repair pass.

### fsck is slow on large caches

**Causes:** Walking all NAR files in storage (phase 1d) and checking each DB entry against storage (phase 1c) are O(n) operations.

**Tips:**

- Run during low-traffic periods
- For S3 storage, fsck performs one `HeadObject` per NAR file — this is billed per request
- For very large caches, consider running the check against a read replica for the database checks

### CDC checks not appearing in summary

CDC checks are only performed when CDC is enabled in the database configuration. If you see only 4 rows in the summary (no `[CDC]` lines), CDC is disabled. Enable it via the `serve` command with the appropriate `--cache-cdc-*` flags.

## Related Documentation

- <a class="reference-link" href="Backup%20Restore.md">Backup Restore</a> - Back up before repairing data loss
- <a class="reference-link" href="NarInfo%20Migration.md">NarInfo Migration</a> - Migrate narinfo metadata to database
- <a class="reference-link" href="NAR%20to%20Chunks%20Migration.md">NAR to Chunks Migration</a> - Migrate NAR files to content-defined chunks
- <a class="reference-link" href="../Configuration/Database.md">Database</a> - Database configuration
- <a class="reference-link" href="../Configuration/Storage.md">Storage</a> - Storage backend configuration
