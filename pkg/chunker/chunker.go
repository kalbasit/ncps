package chunker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/kalbasit/fastcdc"
)

// Chunk represents a single content-defined chunk.
type Chunk struct {
	Hash   string // SHA-256 hash of chunk content
	Offset int64  // Offset in original stream
	Size   uint32 // Chunk size in bytes
}

// Chunker interface for content-defined chunking.
type Chunker interface {
	// Chunk splits the reader into content-defined chunks.
	// Returns two channels: one for yielding chunks and one for yielding errors.
	Chunk(ctx context.Context, r io.Reader) (<-chan Chunk, <-chan error)
}

// CDCChunker implements FastCDC algorithm.
type CDCChunker struct {
	minSize uint32
	avgSize uint32
	maxSize uint32
	pool    *fastcdc.ChunkerPool
}

// NewCDCChunker returns a new CDCChunker.
func NewCDCChunker(minSize, avgSize, maxSize uint32) (*CDCChunker, error) {
	pool, err := fastcdc.NewChunkerPool(
		fastcdc.WithMinSize(minSize),
		fastcdc.WithTargetSize(avgSize),
		fastcdc.WithMaxSize(maxSize),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create chunker pool: %w", err)
	}

	return &CDCChunker{
		minSize: minSize,
		avgSize: avgSize,
		maxSize: maxSize,
		pool:    pool,
	}, nil
}

// Chunk splits the reader into content-defined chunks.
func (c *CDCChunker) Chunk(ctx context.Context, r io.Reader) (<-chan Chunk, <-chan error) {
	chunksChan := make(chan Chunk)
	errChan := make(chan error, 1)

	go func() {
		defer close(chunksChan)

		// Create a FastCDC chunker
		fcdc, err := c.pool.Get(r)
		if err != nil {
			errChan <- fmt.Errorf("error getting fcdc chunker from pool: %w", err)

			return
		}
		defer c.pool.Put(fcdc)

		var offset int64

		for {
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()

				return
			default:
				chunk, err := fcdc.Next()
				if err != nil {
					if err == io.EOF {
						return
					}

					errChan <- fmt.Errorf("error getting next chunk: %w", err)

					return
				}

				// Compute SHA-256 hash of the chunk data
				h := sha256.Sum256(chunk.Data)
				hashStr := hex.EncodeToString(h[:])

				select {
				case <-ctx.Done():
					errChan <- ctx.Err()

					return
				case chunksChan <- Chunk{
					Hash:   hashStr,
					Offset: offset,
					//nolint:gosec // G115: Chunk size is bounded by maxSize (uint32)
					Size: uint32(len(chunk.Data)),
				}:
					offset += int64(len(chunk.Data))
				}
			}
		}
	}()

	return chunksChan, errChan
}
