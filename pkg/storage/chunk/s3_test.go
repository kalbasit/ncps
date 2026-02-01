package chunk_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/lock"
	"github.com/kalbasit/ncps/pkg/lock/local"
	"github.com/kalbasit/ncps/pkg/s3"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testhelper"
)

func TestS3Store_Integration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg := testhelper.S3TestConfig(t)
	if cfg == nil {
		return
	}

	store, err := chunk.NewS3Store(ctx, *cfg, local.NewLocker())
	require.NoError(t, err)

	t.Run("put and get chunk", func(t *testing.T) {
		t.Parallel()

		hash := "test-hash-s3-1"
		content := "s3 chunk content"

		created, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)

		defer func() {
			err := store.DeleteChunk(ctx, hash)
			assert.NoError(t, err)
		}()

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

		hash := "test-hash-s3-2"
		content := "s3 chunk content"

		created1, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created1)

		defer func() {
			err := store.DeleteChunk(ctx, hash)
			assert.NoError(t, err)
		}()

		created2, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.False(t, created2)
	})

	t.Run("get non-existent chunk", func(t *testing.T) {
		t.Parallel()

		hash := "non-existent-s3"
		_, err := store.GetChunk(ctx, hash)
		require.ErrorIs(t, err, chunk.ErrNotFound)
	})

	t.Run("delete chunk idempotency", func(t *testing.T) {
		t.Parallel()

		hash := "test-hash-s3-idempotency"
		content := "s3 chunk content idempotency"

		// Delete non-existent chunk should not return error
		err := store.DeleteChunk(ctx, hash)
		require.NoError(t, err)

		// Put and then delete twice
		created, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)

		err = store.DeleteChunk(ctx, hash)
		require.NoError(t, err)

		err = store.DeleteChunk(ctx, hash)
		require.NoError(t, err)
	})
}

func TestS3Store_PutChunk_RaceCondition(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg := testhelper.S3TestConfig(t)
	if cfg == nil {
		return
	}

	// For now, we pass nil for locker to reproduce the race condition.
	// We will update it to pass a locker once we update the signature.
	store, err := chunk.NewS3Store(ctx, *cfg, local.NewLocker())
	require.NoError(t, err)

	hash := "test-hash-race"
	content := []byte("race condition content")

	defer func() {
		_ = store.DeleteChunk(ctx, hash)
	}()

	const numGoRoutines = 10

	results := make(chan bool, numGoRoutines)
	errors := make(chan error, numGoRoutines)

	for i := 0; i < numGoRoutines; i++ {
		go func() {
			created, err := store.PutChunk(ctx, hash, content)
			results <- created

			errors <- err
		}()
	}

	createdCount := 0

	for i := 0; i < numGoRoutines; i++ {
		err := <-errors
		require.NoError(t, err)

		if <-results {
			createdCount++
		}
	}

	// The contract says true if chunk was new. In a race condition WITHOUT locking,
	// multiple goroutines might see created: true.
	// We want to ensure only ONE goroutine gets created: true.
	assert.Equal(t, 1, createdCount, "Only one goroutine should have created the chunk")
}

func TestNewS3Store_Validation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("missing bucket", func(t *testing.T) {
		t.Parallel()

		cfg := s3.Config{
			Endpoint:        "http://localhost:9000",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		}
		_, err := chunk.NewS3Store(ctx, cfg, local.NewLocker())
		assert.Error(t, err)
	})

	t.Run("invalid endpoint", func(t *testing.T) {
		t.Parallel()

		cfg := s3.Config{
			Bucket:          "test-bucket",
			Endpoint:        "invalid-endpoint",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		}
		_, err := chunk.NewS3Store(ctx, cfg, local.NewLocker())
		assert.Error(t, err)
	})

	t.Run("bucket not found", func(t *testing.T) {
		t.Parallel()

		cfg := testhelper.S3TestConfig(t)
		if cfg == nil {
			return
		}

		cfg.Bucket = "non-existent-bucket-ncps-test"
		_, err := chunk.NewS3Store(ctx, *cfg, local.NewLocker())
		assert.ErrorIs(t, err, chunk.ErrBucketNotFound)
	})
}

func TestS3Store_ChunkPath(t *testing.T) {
	t.Parallel()

	// Since we can't easily access the unexported method without export_test.go
	// and we are in chunk_test package, we use a trick or just test through PutChunk/GetChunk
	// but those already use integration.
	// However, we added export_test.go which is in package chunk.
	// Wait, TestS3Store_ChunkPath should be in package chunk to test it directly.
	// Or we can just call PutChunk with a short hash.

	ctx := context.Background()

	cfg := testhelper.S3TestConfig(t)
	if cfg == nil {
		return
	}

	store, err := chunk.NewS3Store(ctx, *cfg, local.NewLocker())
	require.NoError(t, err)

	t.Run("short hash", func(t *testing.T) {
		hash := "a"
		content := "short hash content"

		created, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)

		defer func() {
			_ = store.DeleteChunk(ctx, hash)
		}()

		has, err := store.HasChunk(ctx, hash)
		require.NoError(t, err)
		assert.True(t, has)
	})
}

type mockLocker struct {
	lockErr error
}

func (m *mockLocker) Lock(_ context.Context, _ string, _ time.Duration) error {
	return m.lockErr
}

func (m *mockLocker) Unlock(_ context.Context, _ string) error {
	return nil
}

func (m *mockLocker) TryLock(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return m.lockErr == nil, m.lockErr
}

func TestS3Store_PutChunk_LockFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cfg := testhelper.S3TestConfig(t)
	if cfg == nil {
		return
	}

	store, err := chunk.NewS3Store(ctx, *cfg, local.NewLocker())
	require.NoError(t, err)

	// Cast to access internal fields (using export_test.go)
	s := store.(interface {
		SetLocker(lock.Locker)
	})

	//nolint:err113
	expectedErr := errors.New("lock failure")
	s.SetLocker(&mockLocker{lockErr: expectedErr})

	_, err = store.PutChunk(ctx, "test-hash", []byte("content"))
	require.ErrorIs(t, err, expectedErr)
	assert.Contains(t, err.Error(), "error acquiring lock for chunk put")
}
