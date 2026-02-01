# Content-Defined Chunking (CDC)

> [!CAUTION]
> **EXPERIMENTAL FEATURE**
> Content-Defined Chunking is currently an experimental feature and may undergo significant changes in future releases. Use with caution in production environments.

## Overview

Content-Defined Chunking (CDC) is an advanced deduplication technique that allows `ncps` to significantly reduce storage usage by identifying and sharing common data across different NAR files.

In a traditional Nix cache, each store path is stored as a single NAR file. Even if two packages share many identical files (e.g., they share common libraries or base layers), they are stored as two separate, complete NAR files. This results in significant storage redundancy.

CDC solves this by splitting NAR files into smaller, variable-sized chunks based on their content rather than fixed offsets.

## How It Works

`ncps` uses the **FastCDC** algorithm to process NAR files:

1. **Chunking**: When a NAR file is fetched from an upstream cache, `ncps` passes it through the CDC chunker. The chunker identifies "natural" boundaries in the data stream to split the file into variable-sized chunks.
1. **Hashing**: Each chunk is hashed (using SHA-256) to create a unique identifier based on its content.
1. **Deduplication**: If a chunk with the same hash already exists in the store (from another NAR file), `ncps` simply references the existing chunk instead of storing it again.
1. **Assembly**: When a client requests a store path, `ncps` assembles it on-the-fly from its constituent chunks.

### Benefits

- **Storage Efficiency**: Dramatic reduction in storage usage when hosting multiple versions of the same package or packages with shared dependencies.
- **Cross-NAR Deduplication**: Deduplication works across all packages in the cache, not just within a single package.
- **Transfer Efficiency**: Chunks are stored in the same backend as NAR files, benefitting from the same scalability and reliability.

## Configuration

CDC is disabled by default. You can enable it by setting `cache.cdc.enabled` to `true`.

### Basic Configuration

```yaml
cache:
  cdc:
    enabled: true
    # Optional: Tune chunk sizes (defaults shown)
    min: 65536    # 64 KB
    avg: 262144   # 256 KB
    max: 1048576  # 1 MB
```

### Parameters

| Flag | Environment Variable | Default | Description |
| --- | --- | --- | --- |
| `--cache-cdc-enabled` | `CACHE_CDC_ENABLED` | `false` | Enable CDC for deduplication. |
| `--cache-cdc-min` | `CACHE_CDC_MIN` | `65536` | Minimum chunk size in bytes. |
| `--cache-cdc-avg` | `CACHE_CDC_AVG` | `262144` | Average (target) chunk size in bytes. |
| `--cache-cdc-max` | `CACHE_CDC_MAX` | `1048576` | Maximum chunk size in bytes. |

## Storage Considerations

When CDC is enabled:

- Chunks are stored in the configured storage backend (local or S3) under a `chunks/` prefix or directory.
- `ncps` maintains a mapping between NAR files and their chunks in the database.
- The `max-size` and LRU cleanup mechanisms still apply to the total size of the cache, including chunks.

## Performance Impact

Processing NAR files through the CDC chunker adds some CPU overhead during the initial download/cache miss. However, the storage savings and potentially reduced I/O (when chunks are already cached) often outweigh this cost in large-scale deployments.

## Related Documentation

- <a class="reference-link" href="../Configuration/Reference.md">Configuration Reference</a>
- <a class="reference-link" href="../Configuration/Storage.md">Storage Configuration</a>
- <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability Setup</a>
