# S3-Compatible Storage

### When to Use

- **High availability deployments**: Multiple ncps instances
- **Cloud-native setups**: Leveraging cloud infrastructure
- **Scalability**: Need storage independent of server resources
- **Redundancy**: Built-in durability and replication

### Supported Providers

- AWS S3
- Garage (self-hosted)
- DigitalOcean Spaces
- Backblaze B2
- Any S3-compatible service

### Configuration

#### AWS S3

**Command-line:**

```
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

#### Garage (or other self-hosted S3)

**Command-line:**

```
ncps serve \
  --cache-storage-s3-bucket=ncps-cache \
  --cache-storage-s3-endpoint=http://garage.example.com:3900 \
  --cache-storage-s3-access-key-id=your-access-key \
  --cache-storage-s3-secret-access-key=your-secret-key \
  --cache-storage-s3-force-path-style=true
```

**Configuration file:**

```yaml
cache:
  storage:
    s3:
      bucket: ncps-cache
      endpoint: http://garage.example.com:3900  # Scheme (http://) is required
      region: us-east-1  # Can be any value for self-hosted S3 servers
      access-key-id: your-access-key
      secret-access-key: your-secret-key
      force-path-style: true  # REQUIRED for Garage and most self-hosted S3 servers
```

**Important:** Garage (and most self-hosted S3 servers) require `force-path-style: true` for proper S3 compatibility. This uses path-style URLs (`http://endpoint/bucket/key`) instead of virtual-hosted-style (`http://bucket.endpoint/key`).

### S3 Configuration Options

| Option | Required | Description | Default |
| --- | --- | --- | --- |
| `bucket` | Yes | S3 bucket name | - |
| `endpoint` | Yes | S3 endpoint URL with scheme (e.g., `https://s3.amazonaws.com`) | - |
| `region` | Yes | AWS region or any value for self-hosted S3 servers | `us-east-1` |
| `access-key-id` | Yes | S3 access key ID | - |
| `secret-access-key` | Yes | S3 secret access key | - |
| `force-path-style` | No | Use path-style URLs (required for Garage and other self-hosted S3 servers) | `false` |

**Endpoint Scheme Requirement:**

- The endpoint **must** include a scheme (`https://` or `http://`)
- Examples: `https://s3.amazonaws.com`, `http://garage:3900`
- The scheme determines whether SSL/TLS is used

### S3 Bucket Setup

#### AWS S3

```
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

```
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

#### Garage

```
# Create a bucket and an access key using the Garage CLI.
# Run these commands on the Garage server (or via `garage` over the admin API).
garage bucket create ncps-cache
garage key import --yes your-access-key your-secret-key
garage bucket allow --read --write --owner ncps-cache --key your-access-key
```

See the [Garage documentation](https://garagehq.deuxfleurs.fr/documentation/) for the full set of cluster, bucket, and key management commands.

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

```
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:PutObject",
- Use TLS for data in transit (by using an `https://` endpoint)
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
