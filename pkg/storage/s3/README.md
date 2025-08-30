# S3 Storage Implementation

This package provides an S3-compatible storage backend for ncps (Nix Cache Proxy Server). It implements the same interface as the local storage but uses S3 or S3-compatible services like MinIO for backend storage.

## Features

- **S3 Compatibility**: Works with AWS S3 and any S3-compatible service (MinIO, Ceph, etc.)
- **Drop-in Replacement**: Implements the same interface as local storage
- **Configurable**: Supports custom endpoints, regions, and authentication
- **MinIO Support**: Optimized for MinIO with path-style addressing
- **Telemetry**: Includes OpenTelemetry tracing for monitoring

## Configuration

The S3 storage is configured using the `Config` struct:

```go
type Config struct {
    Bucket          string // S3 bucket name (required)
    Region          string // AWS region (optional, auto-detected if empty)
    Endpoint        string // Custom endpoint URL (for MinIO, etc.)
    AccessKeyID     string // Access key for authentication (required)
    SecretAccessKey string // Secret key for authentication (required)
    UsePathStyle    bool   // Force path-style addressing (required for MinIO)
    DisableSSL      bool   // Disable SSL/TLS (for local development)
}
```

## Usage

### Basic S3 Usage

```go
import (
    "context"
    "github.com/kalbasit/ncps/pkg/storage/s3"
)

ctx := context.Background()

// Create S3 configuration
cfg := s3.Config{
    Bucket:          "my-nix-cache",
    Region:          "us-west-2",
    AccessKeyID:     "your-access-key",
    SecretAccessKey: "your-secret-key",
}

// Create S3 store
store, err := s3.New(ctx, cfg)
if err != nil {
    log.Fatalf("Failed to create S3 store: %v", err)
}

// Use the store - it implements the storage.Store interface
exists := store.HasNarInfo(ctx, "abc123")
```

### MinIO Usage

```go
// Create MinIO configuration
cfg := s3.Config{
    Bucket:          "my-nix-cache",
    Endpoint:        "http://localhost:9000",
    AccessKeyID:     "minioadmin",
    SecretAccessKey: "minioadmin",
    UsePathStyle:    true,  // Required for MinIO
    DisableSSL:      true,  // For local development
}

store, err := s3.New(ctx, cfg)
```

## Storage Structure

The S3 storage organizes data in the following structure within the bucket:

```
bucket/
├── config/
│   └── cache.key          # Secret key for signing
└── store/
    ├── narinfo/
    │   └── a/ab/abc123.narinfo  # NarInfo files with sharding
    └── nar/
        └── a/ab/abc123.nar      # NAR files with sharding
```

## Key Features

### Sharding
Files are automatically sharded using the first 1-2 characters of their hash to prevent too many files in a single directory, which can cause performance issues with S3.

### Error Handling
The implementation properly handles S3-specific errors:
- `NoSuchKey` errors are converted to `storage.ErrNotFound`
- Configuration validation ensures required fields are provided
- Bucket access is verified during initialization

### Telemetry
All operations include OpenTelemetry tracing with relevant attributes:
- Operation names (e.g., "s3.GetNarInfo")
- Object keys and paths
- Hash values and URLs

## Testing

The package includes comprehensive tests with a mock S3 client:

```bash
go test ./pkg/storage/s3/...
```

## Dependencies

- `github.com/aws/aws-sdk-go-v2` - AWS SDK v2 for Go
- `github.com/aws/aws-sdk-go-v2/service/s3` - S3 service client
- `github.com/aws/aws-sdk-go-v2/config` - AWS configuration

## Migration from Local Storage

To migrate from local storage to S3 storage:

1. Create an S3 bucket or MinIO instance
2. Configure the S3 storage with appropriate credentials
3. Replace the local storage initialization with S3 storage
4. The rest of your application code remains unchanged

```go
// Before (local storage)
localStore, err := local.New(ctx, "/path/to/local/storage")

// After (S3 storage)
s3Store, err := s3.New(ctx, s3.Config{
    Bucket:          "my-nix-cache",
    AccessKeyID:     "your-key",
    SecretAccessKey: "your-secret",
})
```

## Security Considerations

- Store credentials securely (environment variables, IAM roles, etc.)
- Use IAM policies to restrict bucket access
- Consider using temporary credentials for production
- Enable bucket versioning for data protection
- Use appropriate bucket policies for access control

