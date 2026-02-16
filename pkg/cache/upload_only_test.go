package cache_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestGetNar_UploadOnly(t *testing.T) {
	t.Parallel()

	// Setup necessary components
	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	db, localStore, _, _, cleanup := setupTestComponents(t)
	t.Cleanup(cleanup)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	// Use Nar1 which exists in the upstream
	nu := nar.URL{
		Hash:        testdata.Nar1.NarHash,
		Compression: nar.CompressionTypeXz,
	}

	// First verify we can fetch it normally (sanity check)
	// We use a separate context for this to avoid any interference
	t.Run("sanity check - normal fetch works", func(t *testing.T) {
		t.Parallel()

		size, reader, err := c.GetNar(context.Background(), nu)
		require.NoError(t, err)
		require.NotNil(t, reader)
		reader.Close()
		// size can be -1 if streaming from upstream, or > 0 if known
		if size != -1 {
			assert.Positive(t, size)
		}
	})

	// Now try with UploadOnly
	t.Run("upload only - should fail if not in local store", func(t *testing.T) {
		t.Parallel()

		// Ensure it's not in the local store first (might have been pulled by sanity check)
		// Since we share the cache instance and store in the test setup, we need to be careful.
		// Actually, the sanity check pulled it into the store.
		// So let's pick another NAR, say Nar2, for this specific test case.
		nu2 := nar.URL{
			Hash:        testdata.Nar2.NarHash,
			Compression: nar.CompressionTypeXz,
		}

		ctx := cache.WithUploadOnly(context.Background())
		_, _, err := c.GetNar(ctx, nu2)
		assert.ErrorIs(t, err, storage.ErrNotFound,
			"should return ErrNotFound when item is only upstream and UploadOnly is set")
	})
}

func TestGetNarInfo_UploadOnly(t *testing.T) {
	t.Parallel()

	// Setup necessary components
	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	db, localStore, _, _, cleanup := setupTestComponents(t)
	t.Cleanup(cleanup)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, "")
	require.NoError(t, err)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	// Wait for upstream caches to become available
	<-c.GetHealthChecker().Trigger()

	// Use Nar2 which exists upstream but not locally yet
	hash := testdata.Nar2.NarInfoHash

	// Try with UploadOnly
	t.Run("upload only - should fail if not in local store", func(t *testing.T) {
		t.Parallel()

		ctx := cache.WithUploadOnly(context.Background())
		_, err := c.GetNarInfo(ctx, hash)
		assert.ErrorIs(t, err, storage.ErrNotFound,
			"should return ErrNotFound when item is only upstream and UploadOnly is set")
	})
}
