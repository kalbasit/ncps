package chunk

import (
	"context"
	"errors"
	"io"
)

var (
	// ErrNotFound is returned if the chunk was not found.
	ErrNotFound = errors.New("chunk not found")

	// ErrAlreadyExists is returned if the chunk already exists.
	ErrAlreadyExists = errors.New("chunk already exists")
)

// Store represents a storage backend for chunks.
type Store interface {
	// HasChunk checks if a chunk exists.
	// Returns error for I/O or connection failures.
	HasChunk(ctx context.Context, hash string) (bool, error)

	// GetChunk retrieves a chunk by hash.
	// NOTE: The caller must close the returned io.ReadCloser!
	GetChunk(ctx context.Context, hash string) (io.ReadCloser, error)

	// PutChunk stores a chunk. Returns true if chunk was new.
	PutChunk(ctx context.Context, hash string, data []byte) (bool, error)

	// DeleteChunk removes a chunk.
	DeleteChunk(ctx context.Context, hash string) error
}
