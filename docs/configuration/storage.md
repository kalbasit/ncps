[Home](../../README.md) > [Documentation](../README.md) > [Configuration](README.md) > Storage

# Storage Configuration

Configure ncps storage backends: local filesystem or S3-compatible storage.

## Overview

ncps supports two storage backends for storing NAR files and other cache data:

- **Local Filesystem**: Traditional file-based storage
- **S3-Compatible**: AWS S3, MinIO, and other S3-compatible services

**Note:** You must choose exactly ONE storage backend. You cannot use both simultaneously.

## Local Filesystem Storage

### When to Use

- **Single-instance deployments**: One ncps server
- **Simple setup**: No external dependencies
- **Low latency**: Direct disk I/O
- **Testing and development**

### Configuration

**Command-line:**

```bash
ncps serve --cache-storage-local=/var/lib/ncps
```

**Configuration file:**

```yaml
cache:
  storage:
    local: /var/lib/ncps
```

**Environment variable:**

```bash
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

- **Writable directory**: ncps user must have read/write access
- **Sufficient space**: Plan for cache growth (recommend 50GB-1TB)
- **Fast disk**: SSD recommended for better performance

### Permissions

```bash
# Create directory with correct permissions
sudo mkdir -p /var/lib/ncps
sudo chown ncps:ncps /var/lib/ncps
sudo chmod 0755 /var/lib/ncps
```

### Performance Considerations

**Pros:**

- Fast (local disk I/O)
- No network latency
- Simple to manage

**Cons:**

- Limited to single server's disk
- No built-in redundancy
- Not suitable for HA deployments

## S3-Compatible Storage

### When to Use

- **High availability deployments**: Multiple ncps instances
- **Cloud-native setups**: Leveraging cloud infrastructure
- **Scalability**: Need storage independent of server resources
- **Redundancy**: Built-in durability and replication

### Supported Providers

- AWS S3
- MinIO (self-hosted)
- DigitalOcean Spaces
- Backblaze B2
- Any S3-compatible service

### Configuration

#### AWS S3

**Command-line:**

```bash
ncps serve \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=https://s3.amazonaws.com \
  --cache-storage-s3-region=us-east-1 \
  --cache-storage-s3-access-key-id=AKIAIOSFODNN7EXAMPLE \
  --cache-storage-s3-secret-access-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

**Configuration file:**

```yaml
cache:
  storage:
    s3:
      bucket: ncps-cache
      endpoint: https://s3.amazonaws.com  # Scheme (https://) is required
      region: us-east-1
      access-key-id: ${S3_ACCESS_KEY}
      secret-access-key: ${S3_SECRET_KEY}
      # use-ssl is deprecated - specify scheme in endpoint instead
      force-path-style: false  # Use virtual-hosted-style (default for AWS S3)
```

**Note:** The endpoint must include the scheme (`https://` or `http://`). The `use-ssl` option is deprecated in favor of specifying the scheme directly in the endpoint URL.

#### MinIO

**Command-line:**

```bash
ncps serve \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=http://minio.example.com:9000 \
  --cache-storage-s3-access-key-id=minioadmin \
  --cache-storage-s3-secret-access-key=minioadmin \
  --cache-storage-s3-force-path-style=true
```

**Configuration file:**

```yaml
cache:
  storage:
    s3:
      bucket: ncps-cache
      endpoint: http://minio.example.com:9000  # Scheme (http://) is required
      region: us-east-1  # Can be any value for MinIO
      access-key-id: minioadmin
      secret-access-key: minioadmin
      force-path-style: true  # REQUIRED for MinIO
```

**Important:** MinIO requires `force-path-style: true` for proper S3 compatibility. This uses path-style URLs (`http://endpoint/bucket/key`) instead of virtual-hosted-style (`http://bucket.endpoint/key`).

### S3 Configuration Options

| Option | Required | Description | Default |
|--------|----------|-------------|---------|
| `bucket` | Yes | S3 bucket name | - |
| `endpoint` | Yes | S3 endpoint URL with scheme (e.g., `https://s3.amazonaws.com`) | - |
| `region` | Yes | AWS region or any value for MinIO | `us-east-1` |
| `access-key-id` | Yes | S3 access key ID | - |
| `secret-access-key` | Yes | S3 secret access key | - |
| `force-path-style` | No | Use path-style URLs (required for MinIO) | `false` |

**Endpoint Scheme Requirement:**

- The endpoint **must** include a scheme (`https://` or `http://`)
- Examples: `https://s3.amazonaws.com`, `http://minio:9000`
- The scheme determines whether SSL/TLS is used

### S3 Bucket Setup

#### AWS S3

```bash
# Create bucket
aws s3 mb s3://ncps-cache --region us-east-1

# Set bucket policy (optional, for private access)
aws s3api put-bucket-policy \
  --bucket ncps-cache \
  --policy file://bucket-policy.json

# Enable versioning (recommended)
aws s3api put-bucket-versioning \
  --bucket ncps-cache \
  --versioning-configuration Status=Enabled
```

Example `bucket-policy.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::ACCOUNT-ID:user/ncps"
      },
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket"
      ],
      "Resource": [
        "arn:aws:s3:::ncps-cache",
        "arn:aws:s3:::ncps-cache/*"
      ]
    }
  ]
}
```

#### MinIO

```bash
# Using mc (MinIO client)
mc alias set myminio http://minio.example.com:9000 minioadmin minioadmin
mc mb myminio/ncps-cache
mc policy set download myminio/ncps-cache  # Or 'private'
```

### S3 Object Structure

```
s3://ncps-cache/
├── config/          # Configuration (secret keys, etc.)
│   └── secret-key
├── nar/             # NAR files
│   └── <hash>.nar.xz
└── narinfo/         # NarInfo metadata files
    └── <hash>.narinfo
```

### Performance Considerations

**Pros:**

- Scalable (independent of server resources)
- Durable (built-in redundancy)
- Multi-instance support (required for HA)
- Geographic replication options

**Cons:**

- Network latency overhead
- Potential costs (AWS S3)
- Requires S3 service setup
- More complex configuration

### Security Best Practices

**IAM Roles (AWS):**

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
        "s3:ListBucket"
      ],
      "Resource": [
        "arn:aws:s3:::ncps-cache",
        "arn:aws:s3:::ncps-cache/*"
      ]
    }
  ]
}
```

**Encryption:**

- Enable server-side encryption (SSE-S3 or SSE-KMS)
- Use TLS for data in transit (`--cache-storage-s3-use-ssl=true`)

**Access Control:**

- Use least-privilege IAM policies
- Enable bucket versioning for recovery
- Consider bucket lifecycle policies for cost optimization

## Comparison

| Feature | Local Storage | S3 Storage |
|---------|---------------|------------|
| **Setup Complexity** | Simple | Moderate |
| **External Dependencies** | None | S3 service required |
| **Performance** | Fast (local I/O) | Network latency |
| **Scalability** | Limited to disk | Unlimited |
| **High Availability** | ❌ Not supported | ✅ Required |
| **Redundancy** | None (unless RAID/NFS) | Built-in |
| **Cost** | Disk only | S3 storage + requests |
| **Best For** | Single-instance, dev/test | HA, production, cloud |

## Migration Between Storage Backends

### From Local to S3

```bash
# 1. Sync data to S3
aws s3 sync /var/lib/ncps/nar s3://ncps-cache/nar/
aws s3 sync /var/lib/ncps/narinfo s3://ncps-cache/narinfo/
aws s3 sync /var/lib/ncps/config s3://ncps-cache/config/

# 2. Update ncps configuration to use S3
# 3. Restart ncps

# 4. Verify and clean up local storage (optional)
rm -rf /var/lib/ncps/nar /var/lib/ncps/narinfo
```

### From S3 to Local

```bash
# 1. Sync data from S3
aws s3 sync s3://ncps-cache/nar/ /var/lib/ncps/nar/
aws s3 sync s3://ncps-cache/narinfo/ /var/lib/ncps/narinfo/
aws s3 sync s3://ncps-cache/config/ /var/lib/ncps/config/

# 2. Update ncps configuration to use local storage
# 3. Restart ncps
```

## Troubleshooting

### Local Storage Issues

**Permission Denied:**

```bash
# Check ownership
ls -la /var/lib/ncps

# Fix ownership
sudo chown -R ncps:ncps /var/lib/ncps
```

**Disk Full:**

```bash
# Check disk usage
df -h /var/lib/ncps

# Configure LRU cleanup
ncps serve --cache-max-size=50G --cache-lru-schedule="0 2 * * *"
```

### S3 Storage Issues

**Access Denied:**

- Verify credentials are correct
- Check IAM policy permissions
- Ensure bucket exists and is in correct region

**Connection Timeout:**

- Check network connectivity to S3 endpoint
- Verify endpoint URL is correct
- Check firewall rules

**Slow Performance:**

- Check network bandwidth
- Consider using S3 Transfer Acceleration (AWS)
- Verify region is geographically close

See the [Troubleshooting Guide](../operations/troubleshooting.md) for more help.

## Next Steps

1. **[Database Configuration](database.md)** - Configure database backend
1. **[Configuration Reference](reference.md)** - All storage options
1. **[High Availability](../deployment/high-availability.md)** - S3 for HA deployments
1. **[Operations Guide](../operations/)** - Monitoring and maintenance

## Related Documentation

- [Configuration Reference](reference.md) - All configuration options
- [Installation Guides](../installation/) - Installation-specific storage setup
- [pkg/storage/s3/README.md](/pkg/storage/s3/README.md) - S3 implementation details
