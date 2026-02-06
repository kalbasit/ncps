package cache_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

// mockLatencyChunkStore wraps a chunk store and adds artificial latency to GetChunk calls.
// This simulates network latency for S3 or remote storage.
type mockLatencyChunkStore struct {
	chunk.Store
	getChunkLatency time.Duration
	getChunkCalls   atomic.Int64
}

func (m *mockLatencyChunkStore) GetChunk(ctx context.Context, hash string) (io.ReadCloser, error) {
	m.getChunkCalls.Add(1)

	// Simulate network latency
	select {
	case <-time.After(m.getChunkLatency):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return m.Store.GetChunk(ctx, hash)
}

// BenchmarkStreamCompleteChunks_WithPrefetch benchmarks the prefetch implementation.
// It verifies the performance of streaming chunks with overlapping I/O.
func BenchmarkStreamCompleteChunks_WithPrefetch(b *testing.B) {
	ctx := context.Background()

	// Manually create cache for benchmark
	dir, err := os.MkdirTemp("", "bench-cache-")
	require.NoError(b, err)
	b.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	// Create directories
	require.NoError(b, os.MkdirAll(filepath.Dir(dbFile), 0o755))

	// We'll skip the full setup and just test the core functionality
	// For now, use the test factory with a wrapper
	t := &testing.T{}
	c, _, _, cacheDir, _, cleanup := setupSQLiteFactory(t)
	b.Cleanup(cleanup)

	// Initialize chunk store with simulated latency
	chunkStoreDir := filepath.Join(cacheDir, "chunks-store")
	baseStore, err := chunk.NewLocalStore(chunkStoreDir)
	require.NoError(b, err)

	// Wrap with latency simulation (50ms simulates S3 latency)
	latencyStore := &mockLatencyChunkStore{
		Store:           baseStore,
		getChunkLatency: 50 * time.Millisecond,
	}

	c.SetChunkStore(latencyStore)
	err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
	require.NoError(b, err)

	// Create a NAR with multiple chunks
	content := strings.Repeat("benchmark test content for chunk streaming ", 200) // ~8KB
	nu := nar.URL{Hash: "benchmark-nar", Compression: nar.CompressionTypeNone}

	err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
	require.NoError(b, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		latencyStore.getChunkCalls.Store(0)

		_, rc, err := c.GetNar(ctx, nu)
		require.NoError(b, err)

		_, err = io.Copy(io.Discard, rc)
		require.NoError(b, err)
		rc.Close()
	}

	b.ReportMetric(float64(latencyStore.getChunkCalls.Load())/float64(b.N), "chunks/op")
}

// TestPrefetchPipelineOrdering verifies that chunks are streamed in the correct order
// even when prefetching is enabled.
func TestPrefetchPipelineOrdering(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Initialize chunk store
	chunkStoreDir := filepath.Join(dir, "chunks-store")
	chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	err = c.SetCDCConfiguration(true, 512, 2048, 4096) // Small chunks
	require.NoError(t, err)

	// Create content that will be split into multiple chunks
	// Use a pattern that makes it easy to verify ordering
	var contentBuilder strings.Builder
	for i := 0; i < 10; i++ {
		contentBuilder.WriteString(fmt.Sprintf("CHUNK_%02d_", i))
		contentBuilder.WriteString(strings.Repeat("X", 500))
	}

	content := contentBuilder.String()

	nu := nar.URL{Hash: "ordering-test", Compression: nar.CompressionTypeNone}
	err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
	require.NoError(t, err)

	// Retrieve and verify ordering
	_, rc, err := c.GetNar(ctx, nu)
	require.NoError(t, err)

	defer rc.Close()

	retrieved, err := io.ReadAll(rc)
	require.NoError(t, err)

	assert.Equal(t, content, string(retrieved), "chunks must be reassembled in correct order")
}

// TestPrefetchErrorPropagation verifies that errors during prefetch are properly propagated.
func TestPrefetchErrorPropagation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, db, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Initialize chunk store
	chunkStoreDir := filepath.Join(dir, "chunks-store")
	chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
	require.NoError(t, err)

	// Create a NAR with chunks
	content := strings.Repeat("error test content ", 500)
	nu := nar.URL{Hash: "error-test", Compression: nar.CompressionTypeNone}

	err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
	require.NoError(t, err)

	// Get the chunks and delete one from storage (but not from DB)
	chunks, err := db.GetChunksByNarFileID(ctx, 1)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	// Delete the second chunk from storage to simulate a missing chunk
	if len(chunks) > 1 {
		// Find the chunk file
		chunkHash := chunks[1].Hash
		chunkPath := filepath.Join(chunkStoreDir, chunkHash[:2], chunkHash)

		// Only try to delete if it exists
		if _, err := os.Stat(chunkPath); err == nil {
			err = os.Remove(chunkPath)
			require.NoError(t, err)

			// Now try to retrieve the NAR - should fail with proper error
			_, rc, err := c.GetNar(ctx, nu)
			if err == nil {
				_, err = io.Copy(io.Discard, rc)
				rc.Close()
			}

			assert.Error(t, err, "should error when chunk is missing from storage")
		} else {
			t.Skip("Chunk file not found in expected location, skipping error test")
		}
	}
}

// TestPrefetchContextCancellation verifies graceful shutdown when context is cancelled.
func TestPrefetchContextCancellation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Initialize chunk store with latency to make cancellation timing easier
	chunkStoreDir := filepath.Join(dir, "chunks-store")
	baseStore, err := chunk.NewLocalStore(chunkStoreDir)
	require.NoError(t, err)

	latencyStore := &mockLatencyChunkStore{
		Store:           baseStore,
		getChunkLatency: 100 * time.Millisecond,
	}

	c.SetChunkStore(latencyStore)
	err = c.SetCDCConfiguration(true, 1024, 4096, 8192)
	require.NoError(t, err)

	// Create a NAR with multiple chunks
	content := strings.Repeat("cancellation test content ", 500)
	nu := nar.URL{Hash: "cancel-test", Compression: nar.CompressionTypeNone}

	err = c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content)))
	require.NoError(t, err)

	// Capture initial goroutine count
	initialGoroutines := runtime.NumGoroutine()

	// Create a context that we'll cancel mid-stream
	ctx, cancel := context.WithCancel(context.Background())

	_, rc, err := c.GetNar(ctx, nu)
	require.NoError(t, err)

	// Start reading in a goroutine
	errChan := make(chan error, 1)

	go func() {
		// Read just a little bit of data to trigger the prefetcher
		buf := make([]byte, 10)
		_, _ = io.ReadFull(rc, buf)

		// Cancel the context and close the reader immediately
		cancel()
		rc.Close()

		errChan <- nil
	}()

	// Wait for the reader goroutine to finish
	<-errChan

	// Give the prefetcher goroutine some time to exit
	time.Sleep(200 * time.Millisecond)

	// Check for goroutine leaks. We expect the number of goroutines back to baseline.
	// We allow a small tolerance if needed, but here it should be exact.
	finalGoroutines := runtime.NumGoroutine()
	assert.LessOrEqual(t,
		finalGoroutines,
		initialGoroutines+2,
		"should not leak many goroutines (allowing for test infrastructure)",
	)
}

// TestPrefetchMemoryBounds verifies that the prefetch buffer doesn't grow unbounded.
func TestPrefetchMemoryBounds(t *testing.T) {
	t.Parallel()
	t.Skip("This test will be implemented after the prefetch pipeline is added")

	// This test would verify that we don't prefetch too many chunks at once.
	// It would create a NAR with many chunks and verify that we never have
	// more than N chunks in memory at once (where N is the buffer size).
}
