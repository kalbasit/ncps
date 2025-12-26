# S3 Storage Implementation

This package provides an S3-compatible storage backend for ncps (Nix Cache Proxy Server). It implements the same interface as the local storage but uses S3 or S3-compatible services like MinIO for backend storage.

## Features

- **S3 Compatibility**: Works with AWS S3 and any S3-compatible service (MinIO, Ceph, etc.)
- **Drop-in Replacement**: Implements the same interface as local storage
- **Configurable**: Supports custom endpoints, regions, and authentication
- **MinIO Optimized**: Built using the MinIO Go SDK for optimal compatibility
- **Telemetry**: Includes OpenTelemetry tracing for monitoring

## Configuration

The S3 storage is configured using the `Config` struct:

```go
type Config struct {
    Bucket          string // S3 bucket name (required)
    Region          string // AWS region (optional, can be empty for MinIO)
    Endpoint        string // S3 endpoint URL without scheme (required)
    AccessKeyID     string // Access key for authentication (required)
    SecretAccessKey string // Secret key for authentication (required)
    UseSSL          bool   // Enable SSL/TLS (default: false)
}
```

### Important Notes

- **Endpoint Format**: The endpoint should be provided **without** the URL scheme (e.g., `"localhost:9000"` or `"s3.us-west-2.amazonaws.com"`). Use the helper functions `GetEndpointWithoutScheme()` and `IsHTTPS()` if you need to parse endpoints with schemes.
- **Region**: Optional for MinIO, but typically required for AWS S3.
- **UseSSL**: Set to `true` for AWS S3 and production environments, `false` for local development.

## Usage

### MinIO Usage (Local Development)

```go
import (
    "context"
    "github.com/kalbasit/ncps/pkg/storage/s3"
)

ctx := context.Background()

// Create MinIO configuration
cfg := s3.Config{
    Bucket:          "my-nix-cache",
    Endpoint:        "localhost:9000",      // Without scheme
    AccessKeyID:     "minioadmin",
    SecretAccessKey: "minioadmin",
    UseSSL:          false,                 // For local development
}

// Create S3 store
store, err := s3.New(ctx, cfg)
if err != nil {
    log.Fatalf("Failed to create S3 store: %v", err)
}

// Use the store - it implements the storage.Store interface
exists := store.HasNarInfo(ctx, "abc123")
```

### AWS S3 Usage

```go
// Create AWS S3 configuration
cfg := s3.Config{
    Bucket:          "my-nix-cache",
    Region:          "us-west-2",
    Endpoint:        "s3.us-west-2.amazonaws.com",  // Without scheme
    AccessKeyID:     "your-access-key",
    SecretAccessKey: "your-secret-key",
    UseSSL:          true,                          // Always use SSL for AWS
}

store, err := s3.New(ctx, cfg)
```

### Handling Endpoints with Schemes

If you have an endpoint URL that includes the scheme (e.g., from configuration), use the helper functions:

```go
fullEndpoint := "https://s3.us-west-2.amazonaws.com"

cfg := s3.Config{
    Bucket:          "my-nix-cache",
    Endpoint:        s3.GetEndpointWithoutScheme(fullEndpoint),
    AccessKeyID:     "your-access-key",
    SecretAccessKey: "your-secret-key",
    UseSSL:          s3.IsHTTPS(fullEndpoint),
}
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
- Bucket existence is verified during initialization
- Returns `storage.ErrAlreadyExists` when attempting to overwrite existing objects

### Telemetry

All operations include OpenTelemetry tracing with relevant attributes:

- Operation names (e.g., "s3.GetNarInfo", "s3.PutNar")
- Object keys and paths
- Hash values and NAR URLs

### Streaming Uploads

The implementation uses MinIO's streaming upload capability for NAR files (size=-1), allowing efficient uploads of large files without buffering the entire content in memory.

## Testing

The package includes comprehensive tests:

```bash
go test ./pkg/storage/s3/...
```

## Dependencies

- `github.com/minio/minio-go/v7` - MinIO Go SDK for S3 operations
- `github.com/nix-community/go-nix` - Nix-specific types and utilities
- `go.opentelemetry.io/otel` - OpenTelemetry for tracing

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
    Endpoint:        "s3.us-west-2.amazonaws.com",
    AccessKeyID:     "your-key",
    SecretAccessKey: "your-secret",
    UseSSL:          true,
})
```

## Security Considerations

- Store credentials securely (environment variables, IAM roles, etc.)
- Use IAM policies to restrict bucket access (for AWS S3)
- Consider using temporary credentials for production
- Enable bucket versioning for data protection
- Use appropriate bucket policies for access control
- Always use SSL/TLS in production (`UseSSL: true`)
