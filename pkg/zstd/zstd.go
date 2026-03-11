// Package zstd provides compression utilities for the NCPS project.
package zstd

import (
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// writerPool manages a pool of zstd.Encoder instances for reuse.
// This pool is used to reduce allocation overhead when creating multiple
// compression writers. Encoders are reset before being returned to the pool
// and are ready for immediate reuse.
//
// The pool uses the default compression level (no options specified).
// For custom compression levels, create encoders directly with zstd.NewWriter.
//
//nolint:gochecknoglobals
var writerPool = sync.Pool{
	New: func() any {
		// Not providing any options will use the default compression level.
		// The error is ignored as NewWriter(nil) with no options doesn't error.
		enc, _ := zstd.NewWriter(nil)

		return enc
	},
}

// GetWriter retrieves a zstd.Encoder from the pool, or creates a new one
// if the pool is empty. The caller must call PutWriter to return the encoder
// to the pool when done.
//
// Example:
//
//	enc := GetWriter()
//	defer PutWriter(enc)
//	enc.Reset(buf)
//	enc.Write(data)
//	enc.Close()
func GetWriter() *zstd.Encoder {
	return writerPool.Get().(*zstd.Encoder)
}

// PutWriter returns a zstd.Encoder to the pool for reuse.
// The encoder is reset to nil before being returned to the pool.
// If enc is nil, this function is a no-op.
//
// Always pair calls to GetWriter with PutWriter in a defer statement
// or ensure it's called in all code paths.
func PutWriter(enc *zstd.Encoder) {
	if enc != nil {
		enc.Reset(nil)
		writerPool.Put(enc)
	}
}

// maxIdleDecoders is the maximum number of idle zstd.Decoder instances to keep
// in the pool. Each decoder holds ~1-2MB of memory and a background goroutine.
// Using a bounded channel pool (instead of sync.Pool) ensures excess decoders
// are explicitly closed, stopping their background goroutines and freeing memory.
const maxIdleDecoders = 16

// readerPool is a bounded channel pool of zstd.Decoder instances.
//
// A sync.Pool was previously used here, but it caused a memory leak: each
// zstd.Decoder spawns a background goroutine that holds a closure reference back
// to the decoder (circular reference). This prevents the GC from ever marking the
// decoder as unreachable, so runtime.SetFinalizer never fires and the goroutines
// accumulate linearly with throughput.
//
// The bounded channel pool fixes this by calling dec.Close() when the pool is full,
// which explicitly stops the background goroutine and frees its memory.
//
//nolint:gochecknoglobals
var readerPool = make(chan *zstd.Decoder, maxIdleDecoders)

// GetReader retrieves a zstd.Decoder from the pool, or creates a new one
// if the pool is empty. The caller must call PutReader or use NewPooledReader
// for automatic pool management.
//
// Note: Prefer NewPooledReader for automatic resource cleanup.
//
// Example (manual management):
//
//	dec := GetReader()
//	defer PutReader(dec)
//	dec.Reset(reader)
//	data, err := io.ReadAll(dec)
func GetReader() *zstd.Decoder {
	select {
	case dec := <-readerPool:
		return dec
	default:
		// Not providing any options will use the default decompression settings.
		// The error is ignored as NewReader(nil) with no options doesn't error.
		dec, _ := zstd.NewReader(nil)

		return dec
	}
}

// PutReader returns a zstd.Decoder to the pool for reuse.
// The decoder is reset to nil before being returned to the pool.
// If dec is nil, this function is a no-op.
//
// Always pair calls to GetReader with PutReader in a defer statement
// or ensure it's called in all code paths.
func PutReader(dec *zstd.Decoder) {
	if dec == nil {
		return
	}

	_ = dec.Reset(nil)

	select {
	case readerPool <- dec:
		// returned to pool
	default:
		dec.Close() // pool full; close to stop background goroutine and free memory
	}
}

// PooledWriter wraps a zstd.Encoder with automatic pool management.
// When closed, the encoder is automatically returned to the pool.
//
// Example:
//
//	pw := NewPooledWriter(&buf)
//	defer pw.Close()
//	pw.Write(data)
type PooledWriter struct {
	*zstd.Encoder
	w io.Writer
}

// NewPooledWriter creates a new pooled writer that wraps the given io.Writer.
// The returned writer will automatically return its encoder to the pool when closed.
// This is the recommended way to use pooled writers for write operations.
func NewPooledWriter(w io.Writer) *PooledWriter {
	enc := GetWriter()
	enc.Reset(w)

	return &PooledWriter{
		Encoder: enc,
		w:       w,
	}
}

// Close closes the encoder and returns it to the pool.
// Multiple calls to Close are safe and will not panic.
func (pw *PooledWriter) Close() error {
	if pw.Encoder == nil {
		return nil
	}

	err := pw.Encoder.Close()
	PutWriter(pw.Encoder)
	pw.Encoder = nil

	return err
}

// PooledReader wraps a zstd.Decoder with automatic pool management.
// When closed, the decoder is automatically returned to the pool.
//
// Example:
//
//	pr, err := NewPooledReader(compressedReader)
//	if err != nil {
//		return err
//	}
//	defer pr.Close()
//	data, err := io.ReadAll(pr)
type PooledReader struct {
	*zstd.Decoder
	r io.Reader
}

// NewPooledReader creates a new pooled reader that wraps the given io.Reader.
// The returned reader will automatically return its decoder to the pool when closed.
// This is the recommended way to use pooled readers for read operations.
//
// Returns an error if the decoder cannot be reset to read from the given reader.
func NewPooledReader(r io.Reader) (*PooledReader, error) {
	dec := GetReader()
	if err := dec.Reset(r); err != nil {
		PutReader(dec)

		return nil, err
	}

	return &PooledReader{
		Decoder: dec,
		r:       r,
	}, nil
}

// Close closes the reader and returns it to the pool.
// Multiple calls to Close are safe and will not panic.
// Note: The underlying decoder is not explicitly closed, only reset and returned to the pool.
func (pr *PooledReader) Close() error {
	if pr.Decoder == nil {
		return nil
	}

	PutReader(pr.Decoder)
	pr.Decoder = nil

	return nil
}
