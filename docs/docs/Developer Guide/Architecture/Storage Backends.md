# Storage Backends

Implementation details of local and S3 storage backends.

## Storage Interface

All storage backends implement:

```go
type ConfigStore interface {
    GetSecretKey() (string, error)
    PutSecretKey(key string) error
}

type NarInfoStore interface {
    Has(hash string) (bool, error)
    Get(hash string) ([]byte, error)
    Put(hash string, data []byte) error
}

type NarStore interface {
    Has(path string) (bool, error)
    Get(path string) (io.ReadCloser, error)
    Put(path string, data io.Reader) error
}
```

## Local Filesystem Backend

**Location:** `pkg/storage/local/`

**Implementation:**

- Files stored directly on filesystem
- Simple directory structure
- Atomic writes using temp files

**Directory Structure:**

```
/var/lib/ncps/
├── config/
│   └── secret-key
├── nar/
│   └── <hash>.nar.xz
└── narinfo/
    └── <hash>.narinfo
```

**Pros:**

- Fast (local I/O)
- Simple implementation
- No external dependencies

**Cons:**

- Not suitable for HA
- Limited to single server

## S3-Compatible Backend

**Location:** `pkg/storage/s3/`

**Implementation:**

- Uses MinIO Go SDK
- Supports all S3-compatible services
- Concurrent-safe

**Object Structure:**

```
s3://bucket/
├── config/secret-key
├── nar/<hash>.nar.xz
└── narinfo/<hash>.narinfo
```

**Pros:**

- Scalable
- Redundant See <a class="reference-link" href="Storage%20Backends/S3%20Storage%20Implementation.md">S3 Storage Implementation</a> for detailed implementation.

**Cons:**

- Network latency
- Requires S3 service

**Implementation Details:** See <a class="reference-link" href="Storage%20Backends/S3%20Storage%20Implementation.md">S3 Storage Implementation</a> for detailed implementation.

## Related Documentation

- [Storage Configuration](../../User%20Guide/Configuration/Storage.md) - Configure storage
- <a class="reference-link" href="Components.md">Components</a> - All components
