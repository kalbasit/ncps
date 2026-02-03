package chunk_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

func TestLocalStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir, err := os.MkdirTemp("", "ncps-chunk-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	store, err := chunk.NewLocalStore(dir)
	require.NoError(t, err)

	t.Run("put and get chunk", func(t *testing.T) {
		t.Parallel()

		hash := "test-hash-1"
		content := "chunk content"

		created, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)

		has, err := store.HasChunk(ctx, hash)
		require.NoError(t, err)
		assert.True(t, has)

		rc, err := store.GetChunk(ctx, hash)
		require.NoError(t, err)

		defer rc.Close()

		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, content, string(data))
	})

	t.Run("duplicate put", func(t *testing.T) {
		t.Parallel()

		hash := "test-hash-2"
		content := "chunk content"

		created1, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created1)

		created2, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.False(t, created2)
	})

	t.Run("get non-existent chunk", func(t *testing.T) {
		t.Parallel()

		hash := "non-existent"
		_, err := store.GetChunk(ctx, hash)
		require.ErrorIs(t, err, chunk.ErrNotFound)
	})

	t.Run("delete chunk cleans up directory", func(t *testing.T) {
		t.Parallel()

		hash := "abcdef123456"
		content := "cleanup test"

		_, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)

		path := filepath.Join(dir, "chunks", hash[:2], hash)
		parentDir := filepath.Dir(path)

		// Verify chunk and parent directory exist
		_, err = os.Stat(path)
		require.NoError(t, err)
		_, err = os.Stat(parentDir)
		require.NoError(t, err)

		// Delete chunk
		err = store.DeleteChunk(ctx, hash)
		require.NoError(t, err)

		// Verify chunk is gone
		_, err = os.Stat(path)
		assert.True(t, os.IsNotExist(err))

		// Verify parent directory is gone (since it should be empty)
		_, err = os.Stat(parentDir)
		assert.True(t, os.IsNotExist(err))
	})

	t.Run("PutChunk concurrent", func(t *testing.T) {
		t.Parallel()

		hash := "concurrent-hash"
		content := "concurrent content"
		numGoroutines := 10

		var (
			wg       sync.WaitGroup
			createds = make(chan bool, numGoroutines)
			errs     = make(chan error, numGoroutines)
		)

		wg.Add(numGoroutines)

		for i := 0; i < numGoroutines; i++ {
			go func() {
				defer wg.Done()

				created, err := store.PutChunk(ctx, hash, []byte(content))

				createds <- created

				errs <- err
			}()
		}

		wg.Wait()
		close(createds)
		close(errs)

		var totalCreated int

		for err := range errs {
			require.NoError(t, err)
		}

		for created := range createds {
			if created {
				totalCreated++
			}
		}

		assert.Equal(t, 1, totalCreated, "Exactly one goroutine should have created the chunk")

		has, err := store.HasChunk(ctx, hash)
		require.NoError(t, err)
		assert.True(t, has)
	})
}
