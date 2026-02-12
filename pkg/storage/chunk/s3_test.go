package chunk_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
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
		content := strings.Repeat("s3 chunk content", 1024)

		created, size, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)
		assert.Greater(t, int64(len(content)), size)

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
		content := strings.Repeat("s3 chunk content", 1024)

		created1, size1, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created1)
		assert.Greater(t, int64(len(content)), size1)

		defer func() {
			err := store.DeleteChunk(ctx, hash)
			assert.NoError(t, err)
		}()

		created2, size2, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.False(t, created2)
		assert.Greater(t, int64(len(content)), size2)
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
		content := strings.Repeat("s3 chunk content idempotency", 1024)

		// Delete non-existent chunk should not return error
		err := store.DeleteChunk(ctx, hash)
		require.NoError(t, err)

		created, size, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)
		assert.Greater(t, int64(len(content)), size)

		err = store.DeleteChunk(ctx, hash)
		require.NoError(t, err)

		err = store.DeleteChunk(ctx, hash)
		require.NoError(t, err)
	})

	t.Run("stored chunk is zstd-compressed in S3", func(t *testing.T) {
		t.Parallel()

		hash := testhelper.MustRandNarHash()

		data := bytes.Repeat([]byte("compressible"), 1024)
		isNew, compressedSize, err := store.PutChunk(ctx, hash, data)
		require.NoError(t, err)
		assert.True(t, isNew)
		assert.Greater(t, int64(len(data)), compressedSize, "compressed size should be less than original")
		assert.Positive(t, compressedSize)

		defer func() {
			_ = store.DeleteChunk(ctx, hash)
		}()
	})

	t.Run("compressed chunk round-trips correctly via S3", func(t *testing.T) {
		t.Parallel()

		hash := testhelper.MustRandNarHash()

		data := []byte("hello from S3 compressed chunk! hello from S3 compressed chunk!")
		_, _, err := store.PutChunk(ctx, hash, data)
		require.NoError(t, err)

		defer func() {
			_ = store.DeleteChunk(ctx, hash)
		}()

		rc, err := store.GetChunk(ctx, hash)
		require.NoError(t, err)

		defer rc.Close()

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		assert.Equal(t, data, got)
	})
}

func TestS3Store_PutSameChunk_RaceCondition(t *testing.T) {
	t.Parallel()
	runRaceConditionTest(t, false)
}

func TestS3Store_PutDifferentChunk_RaceCondition(t *testing.T) {
	t.Parallel()
	runRaceConditionTest(t, true)
}

func runRaceConditionTest(t *testing.T, distinctHashes bool) {
	t.Helper()

	ctx := context.Background()

	cfg := testhelper.S3TestConfig(t)
	if cfg == nil {
		return
	}

	// We pass a local locker to ensure thread safety during the test.
	store, err := chunk.NewS3Store(ctx, *cfg, local.NewLocker())
	require.NoError(t, err)

	const numGoRoutines = 10

	hashes := make(chan string, numGoRoutines)

	defer func() {
		close(hashes)

		for hash := range hashes {
			_ = store.DeleteChunk(ctx, hash)
		}
	}()

	content := []byte(strings.Repeat("race condition content", 1024))
	sharedHash := testhelper.MustRandNarHash()

	results := make(chan bool, numGoRoutines)
	errs := make(chan error, numGoRoutines)

	for range numGoRoutines {
		go func() {
			hash := sharedHash
			if distinctHashes {
				hash = testhelper.MustRandNarHash()
			}

			hashes <- hash

			created, size, err := store.PutChunk(ctx, hash, content)
			results <- created

			assert.Greater(t, int64(len(content)), size)

			errs <- err
		}()
	}

	createdCount := 0

	for range numGoRoutines {
		err := <-errs
		require.NoError(t, err)

		if <-results {
			createdCount++
		}
	}

	if distinctHashes {
		assert.Equal(t, numGoRoutines, createdCount, "All goroutines should have created their unique chunk")
	} else {
		// The contract says true if chunk was new. In a race condition WITHOUT locking,
		// multiple goroutines might see created: true.
		// We want to ensure only ONE goroutine gets created: true.
		assert.Equal(t, 1, createdCount, "Only one goroutine should have created the chunk")
	}
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
		content := strings.Repeat("short hash content", 1024)

		created, size, err := store.PutChunk(ctx, hash, []byte(content))
		require.NoError(t, err)
		assert.True(t, created)
		assert.Greater(t, int64(len(content)), size)

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

	_, size, err := store.PutChunk(ctx, "test-hash", []byte("content"))
	assert.Equal(t, int64(0), size)
	require.ErrorIs(t, err, expectedErr)
	assert.Contains(t, err.Error(), "error acquiring lock for chunk put")
}
