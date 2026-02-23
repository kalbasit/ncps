# CDC

## Content-Defined Chunking (CDC)

> [!CAUTION]
> **EXPERIMENTAL FEATURE** Content-Defined Chunking is currently an experimental feature and may undergo significant changes in future releases. Use with caution in production environments.

## Overview

Content-Defined Chunking (CDC) is an advanced deduplication technique that allows `ncps` to significantly reduce storage usage by identifying and sharing common data across different NAR files.

In a traditional Nix cache, each store path is stored as a single NAR file. Even if two packages share many identical files (e.g., they share common libraries or base layers), they are stored as two separate, complete NAR files. This results in significant storage redundancy.

CDC solves this by splitting NAR files into smaller, variable-sized chunks based on their content rather than fixed offsets.

## How It Works

`ncps` uses the **FastCDC** algorithm to process NAR files:

1. **Preprocessing**: Before chunking, `ncps` decompresses the NAR file if it is compressed (e.g., xz, zstd). CDC always operates on the raw, uncompressed data to maximize cross-NAR deduplication.
1. **Chunking**: The uncompressed data is passed through the [FastCDC chunker](https://github.com/kalbasit/fastcdc). The chunker identifies "natural" content-defined boundaries in the data stream to split the file into variable-sized chunks.
1. **Hashing**: Each chunk is hashed (using [BLAKE3](https://github.com/zeebo/blake3)) to create a unique identifier based on its content.
1. **Deduplication**: If a chunk with the same hash already exists in the store (from another NAR file), `ncps` simply references the existing chunk instead of storing it again.
1. **Compression**: New (non-duplicate) chunks are compressed with **zstd** before being written to the storage backend.
1. **Assembly**: When a client requests a store path, `ncps` assembles it on-the-fly from its constituent chunks, decompressing each chunk and recompressing the stream for the client using the encoding the client prefers (zstd, brotli, gzip, or raw).

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
    # Optional: Tune chunk sizes (recommended values shown)
    min: 16384    # 16 KB
    avg: 65536    # 64 KB
    max: 262144   # 256 KB
```

### Parameters

| Flag | Description | Environment Variable | Default |
| --- | --- | --- | --- |
| `--cache-cdc-enabled` | Enable CDC for deduplication | `CACHE_CDC_ENABLED` | `false` |
| `--cache-cdc-min` | Minimum chunk size in bytes | `CACHE_CDC_MIN` | none (recommended: 16384) |
| `--cache-cdc-avg` | Average (target) chunk size in bytes | `CACHE_CDC_AVG` | none (recommended: 65536) |
| `--cache-cdc-max` | Maximum chunk size in bytes | `CACHE_CDC_MAX` | none (recommended: 262144) |

## Storage Considerations

When CDC is enabled:

- Chunks are stored in the configured storage backend (local or S3) under a `chunk/` prefix or directory.
- `ncps` maintains a mapping between NAR files and their chunks in the database.
- The `max-size` and LRU cleanup mechanisms still apply to the total size of the cache, including chunks.

## Performance Impact

Processing NAR files through the CDC chunker adds some CPU overhead during the initial download/cache miss. However, the storage savings and potentially reduced I/O (when chunks are already cached) often outweigh this cost in large-scale deployments.

## Related Documentation

- <a class="reference-link" href="../Operations/NAR%20to%20Chunks%20Migration.md">NAR to Chunks Migration</a>
- <a class="reference-link" href="../Configuration/Reference.md">Reference</a>
- <a class="reference-link" href="../Configuration/Storage.md">Storage</a>
- <a class="reference-link" href="../Deployment/High%20Availability.md">High Availability</a>
