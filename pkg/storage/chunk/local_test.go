package chunk_test

import (
	"context"
	"io"
	"os"
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

	defer os.RemoveAll(dir)

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
}
