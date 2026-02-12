# ZSTD Pool Management

## Overview

The `pkg/zstd` package provides a `sync.Pool`-based implementation for recycling zstd encoder and decoder instances. This reduces allocation overhead when creating multiple compression/decompression operations, which is especially beneficial in high-throughput scenarios like the NCPS cache server.

## Motivation

Creating new `zstd.Encoder` and `zstd.Decoder` instances is relatively expensive due to internal buffer allocations. When handling many compression/decompression operations (as in chunk storage and HTTP compression), reusing these instances via a pool significantly reduces garbage collection pressure and improves performance.

## Quick Reference

### Import

```go
import "github.com/kalbasit/ncps/pkg/zstd"
```

### Common Patterns

#### Compress Data

```go
pw := zstd.NewPooledWriter(&buf)
defer pw.Close()
pw.Write(data)
```

#### Decompress Data

```go
pr, err := zstd.NewPooledReader(reader)
if err != nil {
    return err
}
defer pr.Close()
data, _ := io.ReadAll(pr)
```

#### One-Shot Encoding

```go
enc := zstd.GetWriter()
defer zstd.PutWriter(enc)
compressed := enc.EncodeAll(data, nil)
```

#### One-Shot Decoding

```go
dec := zstd.GetReader()
defer zstd.PutReader(dec)
dec.Reset(reader)
data, _ := io.ReadAll(dec)
```

### API Cheat Sheet

| Function | Purpose | Returns | Error |
|----------|---------|---------|-------|
| `GetWriter()` | Get encoder from pool | `*zstd.Encoder` | N/A |
| `PutWriter(enc)` | Return encoder to pool | `void` | N/A |
| `GetReader()` | Get decoder from pool | `*zstd.Decoder` | N/A |
| `PutReader(dec)` | Return decoder to pool | `void` | N/A |
| `NewPooledWriter(w)` | Create auto-managed writer | `*PooledWriter` | N/A |
| `NewPooledReader(r)` | Create auto-managed reader | `*PooledReader` | error |
| `pw.Close()` | Close writer, return to pool | `error` | compression error |
| `pr.Close()` | Close reader, return to pool | `error` | nil |

______________________________________________________________________

## API Documentation

### Low-Level API (Manual Management)

For fine-grained control, use the low-level functions:

#### Writer Pool

```go
// Get an encoder from the pool
enc := zstd.GetWriter()
defer zstd.PutWriter(enc)

// Reset the encoder to write to a buffer
var buf bytes.Buffer
enc.Reset(&buf)

// Use the encoder
enc.Write(data)
enc.Close()

// The encoder is automatically reset before being returned to the pool
```

#### Reader Pool

```go
// Get a decoder from the pool
dec := zstd.GetReader()
defer zstd.PutReader(dec)

// Reset the decoder to read from a compressed source
dec.Reset(compressedReader)

// Use the decoder
decompressed, err := io.ReadAll(dec)
```

### High-Level API (Automatic Management)

For simplicity and to avoid resource leaks, use the wrapped types:

#### PooledWriter

```go
import "github.com/kalbasit/ncps/pkg/zstd"

// Create a pooled writer - automatically manages the encoder
pw := zstd.NewPooledWriter(&buf)
defer pw.Close()  // Automatically returns encoder to pool

// Use like a normal zstd encoder
pw.Write(data)
pw.Close()
```

#### PooledReader

```go
import "github.com/kalbasit/ncps/pkg/zstd"

// Create a pooled reader - automatically manages the decoder
pr, err := zstd.NewPooledReader(compressedReader)
if err != nil {
    return err
}
defer pr.Close()  // Automatically returns decoder to pool

// Use like a normal zstd decoder
data, err := io.ReadAll(pr)
```

## Usage Examples

### Example 1: Compressing Multiple Data Chunks

```go
func compressChunks(chunks [][]byte) ([][]byte, error) {
    result := make([][]byte, len(chunks))

    for i, chunk := range chunks {
        var buf bytes.Buffer
        pw := zstd.NewPooledWriter(&buf)

        if _, err := pw.Write(chunk); err != nil {
            pw.Close()
            return nil, err
        }

        if err := pw.Close(); err != nil {
            return nil, err
        }

        result[i] = buf.Bytes()
    }

    return result, nil
}
```

### Example 2: Decompressing Data

```go
func decompressData(compressed []byte) ([]byte, error) {
    pr, err := zstd.NewPooledReader(bytes.NewReader(compressed))
    if err != nil {
        return nil, err
    }
    defer pr.Close()

    return io.ReadAll(pr)
}
```

### Example 3: Direct Encoding (No Streaming)

```go
func quickCompress(data []byte) []byte {
    enc := zstd.GetWriter()
    defer zstd.PutWriter(enc)

    // Use EncodeAll for non-streaming compression
    return enc.EncodeAll(data, nil)
}
```

## Pool Configuration

Both pools use the default zstdion level and settings:

- **WriterPool**: Default compression level (fast but good compression)
- **ReaderPool**: Default decompression settings

For custom zstdion levels or options, create encoders/decoders directly without pooling:

```go
// For custom compression level
enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
if err != nil {
    return err
}
defer enc.Close()
```

## Performance Considerations

1. **Pool Benefits**: Most beneficial when you have many compression/decompression operations
1. **Memory Trade-off**: The pool maintains encoder/decoder instances in memory, ready for reuse
1. **Thread-Safe**: `sync.Pool` is thread-safe and designed for concurrent use
1. **Automatic Cleanup**: Decoders and encoders are reset to a clean state before being returned to the pool

## Integration Points

The zstd pool is used in:

- `pkg/server/server.go` - HTTP response compression
- `pkg/storage/chunk/local.go` - Local chunk storage compression
- `pkg/storage/chunk/s3.go` - S3 chunk storage compression
- Test utilities and helpers

## Migration Guide

To migrate existing code to use the zstd pool:

### Before (Direct Creation)

```go
import "github.com/klauspost/zstd/zstd"

encoder, err := zstd.NewWriter(&buf)
if err != nil {
    return err
}
defer encoder.Close()
encoder.Write(data)
```

### After (Using Pool)

```go
import "github.com/kalbasit/ncps/pkg/zstd"

pw := zstd.NewPooledWriter(&buf)
defer pw.Close()
pw.Write(data)
```

## Best Practices

1. **Always defer Close()**: Ensure pooled resources are returned promptly
1. **Use Wrapped Types**: Prefer `PooledWriter` and `PooledReader` for cleaner code
1. **Handle Errors**: Check errors from Close(), Reset(), and Read/Write operations
1. **One Writer/Reader Per Operation**: Get/release for each independent compression/decompression
1. **Avoid Nested Pools**: Don't hold multiple pooled instances simultaneously unless necessary

## Testing

The zstd pool includes comprehensive tests in `pkg/zstd/zstd_test.go`:

```bash
go test ./pkg/zstd -v -run
```

Tests cover:

- Pool allocation and reuse
- Round-trip compression/decompression
- Error handling
- Resource cleanup
- Concurrent pool access

______________________________________________________________________

## Implementation Details

### Files Created

#### 1. `pkg/zstd/zstd.go`

The main implementation file containing:

- **WriterPool**: A `sync.Pool` managing reusable `zstd.Encoder` instances
- **ReaderPool**: A `sync.Pool` managing reusable `zstd.Decoder` instances

#### 2. `pkg/zstd/zstd_test.go`

Comprehensive test suite covering:

- Pool get/put operations
- Pooled wrapper functionality
- Round-trip compression/decompression
- Error handling
- Multiple close operations
- Nil safety
- EncodeAll pattern support

### Design Decisions

#### Why `sync.Pool`?

- Built into Go standard library
- Thread-safe without explicit locking
- Automatically adjusts to contention
- Zero-copy semantics

#### Why Two APIs?

- **Low-level**: For complex scenarios needing manual control
- **High-level**: For common cases with automatic cleanup
- Recommendation: Use high-level in most cases

#### Why Default Compression Level?

- Covers 99% of use cases
- Custom levels can use direct `zstd.NewWriter()`
- Simpler pool implementation

#### Decoder Reset Pattern

- Decoders are reset but not explicitly closed when returned to pool
- Prevents "decoder used after Close" errors
- Allows safe reuse of pooled decoders
