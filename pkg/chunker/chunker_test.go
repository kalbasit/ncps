package chunker_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/chunker"
)

func TestCDCChunker_Chunk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create some deterministic data
	data := make([]byte, 1024*1024) // 1MB
	for i := range data {
		data[i] = byte(i % 256)
	}

	// Also create a slightly modified version
	modifiedData := make([]byte, len(data))
	copy(modifiedData, data)
	modifiedData[500*1024] = 0xFF // Change one byte in the middle

	chr, err := chunker.NewCDCChunker(2*1024, 64*1024, 256*1024)
	require.NoError(t, err)

	t.Run("deterministic chunking", func(t *testing.T) {
		t.Parallel()

		chunks1, err1 := collectChunks(ctx, chr, bytes.NewReader(data))
		require.NoError(t, err1)

		chunks2, err2 := collectChunks(ctx, chr, bytes.NewReader(data))
		require.NoError(t, err2)

		assert.Len(t, chunks2, len(chunks1))

		for i := range chunks1 {
			assert.Equal(t, chunks1[i].Hash, chunks2[i].Hash)
			assert.Equal(t, chunks1[i].Size, chunks2[i].Size)
			assert.Equal(t, chunks1[i].Offset, chunks2[i].Offset)
		}
	})

	t.Run("resilience to modification", func(t *testing.T) {
		t.Parallel()

		chunksOriginal, err1 := collectChunks(ctx, chr, bytes.NewReader(data))
		require.NoError(t, err1)

		chunksModified, err2 := collectChunks(ctx, chr, bytes.NewReader(modifiedData))
		require.NoError(t, err2)

		// Most chunks should be identical
		identicalCount := 0

		originalHashes := make(map[string]bool)
		for _, c := range chunksOriginal {
			originalHashes[c.Hash] = true
		}

		for _, c := range chunksModified {
			if originalHashes[c.Hash] {
				identicalCount++
			}
		}

		// With 1MB data and 64KB avg size, we expect ~16 chunks.
		// Changing 1 byte should only affect 1-2 chunks.
		assert.Greater(t, identicalCount, len(chunksOriginal)-3)
	})

	t.Run("empty reader", func(t *testing.T) {
		t.Parallel()

		chunks, err := collectChunks(ctx, chr, bytes.NewReader([]byte{}))
		require.NoError(t, err)
		assert.Empty(t, chunks)
	})
}

func collectChunks(ctx context.Context, c chunker.Chunker, r io.Reader) ([]chunker.Chunk, error) {
	chunksChan, errChan := c.Chunk(ctx, r)

	var chunks []chunker.Chunk

	for {
		select {
		case chunk, ok := <-chunksChan:
			if !ok {
				// Producer is done. Check if there's a pending error.
				select {
				case err := <-errChan:
					return nil, err
				default:
					return chunks, nil
				}
			}

			chunks = append(chunks, chunk)
		case err := <-errChan:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
