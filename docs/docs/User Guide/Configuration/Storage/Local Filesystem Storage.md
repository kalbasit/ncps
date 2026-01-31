# Local Filesystem Storage
### When to Use

*   **Single-instance deployments**: One ncps server
*   **Simple setup**: No external dependencies
*   **Low latency**: Direct disk I/O
*   **Testing and development**

### Configuration

**Command-line:**

```
ncps serve --cache-storage-local=/var/lib/ncps
```

**Configuration file:**

```yaml
cache:
  storage:
    local: /var/lib/ncps
```

**Environment variable:**

```
export CACHE_STORAGE_LOCAL=/var/lib/ncps
```

### Directory Structure

Local storage creates the following structure:

```
/var/lib/ncps/
├── config/          # Configuration (secret keys, etc.)
├── nar/             # NAR files
└── narinfo/         # NarInfo metadata files
```

### Requirements

*   **Writable directory**: ncps user must have read/write access
*   **Sufficient space**: Plan for cache growth (recommend 50GB-1TB)
*   **Fast disk**: SSD recommended for better performance

### Permissions

```
# Create directory with correct permissions
sudo mkdir -p /var/lib/ncps
sudo chown ncps:ncps /var/lib/ncps
sudo chmod 0755 /var/lib/ncps
```

### Performance Considerations

**Pros:**

*   Fast (local disk I/O)
*   No network latency
*   Simple to manage

**Cons:**

*   Limited to single server's disk
*   No built-in redundancy
*   Not suitable for HA deployments