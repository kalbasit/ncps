package s3_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	s3config "github.com/kalbasit/ncps/pkg/s3"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/s3"
	"github.com/kalbasit/ncps/testdata"
)

const cacheName = "cache.example.com"

// Integration tests that require a running MinIO server
// These tests are skipped unless NCPS_TEST_S3_ENDPOINT is set

func getTestStore(t *testing.T) *s3.Store {
	t.Helper()

	cfg := getTestConfig(t)
	if cfg == nil {
		return nil
	}

	ctx := newContext()

	store, err := s3.New(ctx, *cfg)
	require.NoError(t, err)

	return store
}

func getTestConfig(t *testing.T) *s3config.Config {
	t.Helper()

	// Skip if S3 test endpoint is not configured
	endpoint := os.Getenv("NCPS_TEST_S3_ENDPOINT")
	bucket := os.Getenv("NCPS_TEST_S3_BUCKET")
	region := os.Getenv("NCPS_TEST_S3_REGION")
	accessKeyID := os.Getenv("NCPS_TEST_S3_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("NCPS_TEST_S3_SECRET_ACCESS_KEY")

	if endpoint == "" || bucket == "" || region == "" || accessKeyID == "" || secretAccessKey == "" {
		t.Skip("Skipping S3 integration test: S3 environment variables not set")

		return nil
	}

	return &s3config.Config{
		Bucket:          bucket,
		Region:          region,
		Endpoint:        endpoint,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		ForcePathStyle:  true, // MinIO requires path-style addressing
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("missing bucket returns error", func(t *testing.T) {
		t.Parallel()

		cfg := s3config.Config{
			Endpoint:        "http://localhost:9000",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		}

		_, err := s3.New(newContext(), cfg)
		assert.ErrorIs(t, err, s3config.ErrBucketRequired)
	})

	t.Run("missing endpoint returns error", func(t *testing.T) {
		t.Parallel()

		cfg := s3config.Config{
			Bucket:          "test-bucket",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		}

		_, err := s3.New(newContext(), cfg)
		assert.ErrorIs(t, err, s3config.ErrEndpointRequired)
	})
}

// Integration tests - require running MinIO instance

//nolint:paralleltest
func TestGetSecretKey_Integration(t *testing.T) {
	// Note: Secret key tests cannot run in parallel as they share the same path
	t.Run("no secret key is present", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		// Make sure it doesn't exist first
		_ = store.DeleteSecretKey(ctx)

		_, err := store.GetSecretKey(ctx)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})
}

//nolint:paralleltest
func TestPutSecretKey_Integration(t *testing.T) {
	// Note: Secret key tests cannot run in parallel as they share the same path
	t.Run("put secret key successfully", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		// Clean up first
		_ = store.DeleteSecretKey(ctx)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteSecretKey(ctx)
		})

		require.NoError(t, store.PutSecretKey(ctx, sk))

		// Verify it was stored
		sk2, err := store.GetSecretKey(ctx)
		require.NoError(t, err)

		assert.Equal(t, sk.String(), sk2.String())
	})

	t.Run("put existing secret key returns error", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteSecretKey(ctx)
		})

		// Clean up first and put the key
		_ = store.DeleteSecretKey(ctx)
		require.NoError(t, store.PutSecretKey(ctx, sk))

		// Try to put again
		sk2, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		err = store.PutSecretKey(ctx, sk2)
		require.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

//nolint:paralleltest
func TestDeleteSecretKey_Integration(t *testing.T) {
	// Note: Secret key tests cannot run in parallel as they share the same path
	t.Run("delete non-existent secret key returns error", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		// Make sure it doesn't exist
		_ = store.DeleteSecretKey(ctx)

		err := store.DeleteSecretKey(ctx)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete existing secret key", func(t *testing.T) {
		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		sk, _, err := signature.GenerateKeypair(cacheName, nil)
		require.NoError(t, err)

		// Clean up first and put the key
		_ = store.DeleteSecretKey(ctx)
		require.NoError(t, store.PutSecretKey(ctx, sk))

		// Delete it
		require.NoError(t, store.DeleteSecretKey(ctx))

		// Verify it's gone
		_, err = store.GetSecretKey(ctx)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})
}

func TestHasNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		// Make sure it doesn't exist
		_ = store.DeleteNarInfo(ctx, hash)

		assert.False(t, store.HasNarInfo(ctx, hash))
	})

	t.Run("narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		assert.True(t, store.HasNarInfo(ctx, hash))
	})
}

func TestGetNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		// Make sure it doesn't exist
		_ = store.DeleteNarInfo(ctx, hash)

		_, err := store.GetNarInfo(ctx, hash)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("narinfo exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		ni2, err := store.GetNarInfo(ctx, hash)
		require.NoError(t, err)

		assert.Equal(t,
			strings.TrimSpace(testdata.Nar1.NarInfoText),
			strings.TrimSpace(ni2.String()),
		)
	})
}

func TestPutNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("put narinfo successfully", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first
		_ = store.DeleteNarInfo(ctx, hash)

		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		// Verify it was stored
		assert.True(t, store.HasNarInfo(ctx, hash))
	})

	t.Run("put existing narinfo returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNarInfo(ctx, hash)
		})

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		// Try to put again
		err = store.PutNarInfo(ctx, hash, ni)
		require.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteNarInfo_Integration(t *testing.T) {
	t.Parallel()

	t.Run("delete non-existent narinfo returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		// Make sure it doesn't exist
		_ = store.DeleteNarInfo(ctx, hash)

		err := store.DeleteNarInfo(ctx, hash)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete existing narinfo", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()
		hash := getUniqueHash(t, testdata.Nar1.NarInfoHash)

		ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
		require.NoError(t, err)

		// Clean up first and put the narinfo
		_ = store.DeleteNarInfo(ctx, hash)
		require.NoError(t, store.PutNarInfo(ctx, hash, ni))

		// Delete it
		require.NoError(t, store.DeleteNarInfo(ctx, hash))

		// Verify it's gone
		assert.False(t, store.HasNarInfo(ctx, hash))
	})
}

func TestHasNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Make sure it doesn't exist
		_ = store.DeleteNar(ctx, narURL)

		assert.False(t, store.HasNar(ctx, narURL))
	})

	t.Run("nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		assert.True(t, store.HasNar(ctx, narURL))
	})
}

func TestGetNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("no nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Make sure it doesn't exist
		_ = store.DeleteNar(ctx, narURL)

		_, _, err := store.GetNar(ctx, narURL)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("nar exists", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		size, r, err := store.GetNar(ctx, narURL)
		require.NoError(t, err)

		defer r.Close()

		content, err := io.ReadAll(r)
		require.NoError(t, err)

		assert.EqualValues(t, len(testdata.Nar1.NarText), size)
		assert.Equal(t, testdata.Nar1.NarText, string(content))
	})
}

func TestPutNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("put nar successfully", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Clean up first
		_ = store.DeleteNar(ctx, narURL)

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		written, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)
		assert.EqualValues(t, len(testdata.Nar1.NarText), written)

		// Verify it was stored
		assert.True(t, store.HasNar(ctx, narURL))
	})

	t.Run("put existing nar returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Register the Clean up
		t.Cleanup(func() {
			//nolint:errcheck
			store.DeleteNar(ctx, narURL)
		})

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		// Try to put again
		_, err = store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.ErrorIs(t, err, storage.ErrAlreadyExists)
	})
}

func TestDeleteNar_Integration(t *testing.T) {
	t.Parallel()

	t.Run("delete non-existent nar returns error", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Make sure it doesn't exist
		_ = store.DeleteNar(ctx, narURL)

		err := store.DeleteNar(ctx, narURL)
		assert.ErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete existing nar", func(t *testing.T) {
		t.Parallel()

		store := getTestStore(t)
		if store == nil {
			return
		}

		ctx := newContext()

		narURL := nar.URL{
			Hash:        getUniqueHash(t, testdata.Nar1.NarHash),
			Compression: testdata.Nar1.NarCompression,
		}

		// Clean up first and put the nar
		_ = store.DeleteNar(ctx, narURL)
		_, err := store.PutNar(ctx, narURL, strings.NewReader(testdata.Nar1.NarText))
		require.NoError(t, err)

		// Delete it
		require.NoError(t, store.DeleteNar(ctx, narURL))

		// Verify it's gone
		assert.False(t, store.HasNar(ctx, narURL))
	})
}

func newContext() context.Context {
	return zerolog.
		New(io.Discard).
		WithContext(context.Background())
}

// getUniqueHash generates a unique hash for testing based on the test name
// This prevents parallel tests from interfering with each other.
func getUniqueHash(t *testing.T, base string) string {
	t.Helper()
	// Use test name to create a unique hash prefix
	// Replace slashes and spaces with underscores for valid hash format
	testName := strings.ReplaceAll(t.Name(), "/", "_")
	testName = strings.ReplaceAll(testName, " ", "_")
	// Combine with base hash to create unique hash
	return testName + "_" + base
}
