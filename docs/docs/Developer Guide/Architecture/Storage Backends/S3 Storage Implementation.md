# S3 Storage Implementation
## S3 Storage Implementation

This package provides an S3-compatible storage backend for ncps (Nix Cache Proxy Server). It implements the same interface as the local storage but uses S3 or S3-compatible services like MinIO for backend storage.

## Features

*   **S3 Compatibility**: Works with AWS S3 and any S3-compatible service (MinIO, Ceph, etc.)
*   **Drop-in Replacement**: Implements the same interface as local storage
*   **Configurable**: Supports custom endpoints, regions, and authentication
*   **MinIO Optimized**: Built using the MinIO Go SDK for optimal compatibility
*   **Telemetry**: Includes OpenTelemetry tracing for monitoring

## Configuration

The S3 storage is configured using the `Config` struct:

```go
type Config struct {
    Bucket          string // S3 bucket name (required)
    Region          string // AWS region (optional, can be empty for MinIO)
    Endpoint        string // S3 endpoint URL with scheme (required)
    AccessKeyID     string // Access key for authentication (required)
    SecretAccessKey string // Secret key for authentication (required)
    ForcePathStyle  bool   // Force path-style addressing (required for MinIO, optional for AWS S3)
}
```

### Important Notes

*   **Endpoint Format**: The endpoint **must** include the URL scheme (e.g., `"http://localhost:9000"` or `"https://s3.us-west-2.amazonaws.com"`). The scheme determines whether SSL/TLS is used.
*   **Region**: Optional for MinIO, but typically required for AWS S3.
*   **ForcePathStyle**: Set to `true` for MinIO and other S3-compatible services that require path-style addressing. Set to `false` for AWS S3 (which uses virtual-hosted-style by default).

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
    Endpoint:        "http://localhost:9000", // Must include scheme
    AccessKeyID:     "minioadmin",
    SecretAccessKey: "minioadmin",
    ForcePathStyle:  true,                    // MinIO requires path-style addressing
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
    Endpoint:        "https://s3.us-west-2.amazonaws.com", // Must include scheme
    AccessKeyID:     "your-access-key",
    SecretAccessKey: "your-secret-key",
    ForcePathStyle:  false,                                // AWS S3 uses virtual-hosted-style
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

*   `NoSuchKey` errors are converted to `storage.ErrNotFound`
*   Configuration validation ensures required fields are provided
*   Bucket existence is verified during initialization
*   Returns `storage.ErrAlreadyExists` when attempting to overwrite existing objects

### Telemetry

All operations include OpenTelemetry tracing with relevant attributes:

*   Operation names (e.g., "s3.GetNarInfo", "s3.PutNar")
*   Object keys and paths
*   Hash values and NAR URLs

### Streaming Uploads

The implementation uses MinIO's streaming upload capability for NAR files (size=-1), allowing efficient uploads of large files without buffering the entire content in memory.

## Testing

The package includes comprehensive tests:

```go
go test ./pkg/storage/s3/...
```

## Dependencies

*   `github.com/minio/minio-go/v7` - MinIO Go SDK for S3 operations
*   `github.com/nix-community/go-nix` - Nix-specific types and utilities
*   `go.opentelemetry.io/otel` - OpenTelemetry for tracing

## Migration from Local Storage

To migrate from local storage to S3 storage:

1.  Create an S3 bucket or MinIO instance
2.  Configure the S3 storage with appropriate credentials
3.  Replace the local storage initialization with S3 storage
4.  The rest of your application code remains unchanged

```go
// Before (local storage)
localStore, err := local.New(ctx, "/path/to/local/storage")

// After (S3 storage)
s3Store, err := s3.New(ctx, s3.Config{
    Bucket:          "my-nix-cache",
    Endpoint:        "https://s3.us-west-2.amazonaws.com",
    AccessKeyID:     "your-key",
    SecretAccessKey: "your-secret",
    ForcePathStyle:  false,
})
```

## Security Considerations

*   Store credentials securely (environment variables, IAM roles, etc.)
*   Use IAM policies to restrict bucket access (for AWS S3)
*   Consider using temporary credentials for production
*   Enable bucket versioning for data protection
*   Use appropriate bucket policies for access control
*   Always use HTTPS in production (e.g., `Endpoint: "https://s3.amazonaws.com"`)