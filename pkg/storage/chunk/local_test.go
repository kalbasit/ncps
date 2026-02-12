package chunk_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testhelper"
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

		hash := testhelper.MustRandNarHash()
		content := strings.Repeat("chunk content", 1024)

		created, size, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)
		assert.Greater(t, int64(len(content)), size)

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

		hash := testhelper.MustRandNarHash()
		content := strings.Repeat("chunk content", 1024)

		created1, size1, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created1)
		assert.Greater(t, int64(len(content)), size1)

		created2, size2, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.False(t, created2)
		assert.Greater(t, int64(len(content)), size2)
	})

	t.Run("get non-existent chunk", func(t *testing.T) {
		t.Parallel()

		hash := testhelper.MustRandNarHash()
		_, err := store.GetChunk(ctx, hash)
		require.ErrorIs(t, err, chunk.ErrNotFound)
	})

	t.Run("delete chunk cleans up directory", func(t *testing.T) {
		t.Parallel()

		hash := testhelper.MustRandNarHash()
		content := strings.Repeat("cleanup test", 1024)

		_, _, err := store.PutChunk(ctx, hash, []byte(content))
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

		hash := testhelper.MustRandNarHash()
		content := strings.Repeat("concurrent content", 1024)
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

				created, _, err := store.PutChunk(ctx, hash, []byte(content))

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

	t.Run("stored chunk is zstd-compressed on disk", func(t *testing.T) {
		t.Parallel()

		// Use highly compressible data (repeated bytes)
		data := bytes.Repeat([]byte("compressible"), 1024)
		isNew, compressedSize, err := store.PutChunk(ctx, "test-hash-compress-1", data)
		require.NoError(t, err)
		assert.True(t, isNew)
		assert.Greater(t, int64(len(data)), compressedSize, "compressed size should be less than original")
		assert.Positive(t, compressedSize, "compressed size should be greater than 0")
	})

	t.Run("compressed chunk round-trips correctly", func(t *testing.T) {
		t.Parallel()

		data := []byte("hello, compressed world! hello, compressed world! hello, compressed world!")
		_, _, err := store.PutChunk(ctx, "test-hash-roundtrip", data)
		require.NoError(t, err)

		rc, err := store.GetChunk(ctx, "test-hash-roundtrip")
		require.NoError(t, err)

		defer rc.Close()

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, data, got)
	})
}
