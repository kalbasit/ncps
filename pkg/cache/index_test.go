package cache_test

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nixcacheindex"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

func TestGenerateIndex(t *testing.T) {
	t.Parallel()

	dir, err := os.MkdirTemp("", "cache-index-test-")
	require.NoError(t, err)

	defer os.RemoveAll(dir)

	dbFile := filepath.Join(dir, "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	db, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	c, err := newTestCache(newContext(), cacheName, db, localStore, localStore, localStore, localStore, "")
	require.NoError(t, err)

	// 1. Insert some NarInfos
	ctx := newContext()

	// Nar1
	err = c.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, io.NopCloser(strings.NewReader(testdata.Nar1.NarInfoText)))
	require.NoError(t, err)

	// Nar2
	err = c.PutNarInfo(ctx, testdata.Nar2.NarInfoHash, io.NopCloser(strings.NewReader(testdata.Nar2.NarInfoText)))
	require.NoError(t, err)

	// 2. Trigger Generation
	err = c.GenerateIndexForTest(ctx)
	require.NoError(t, err)

	// 3. Verify Manifest exists
	manifestPath := filepath.Join(dir, "store", "nix-cache-index", "manifest.json")
	assert.FileExists(t, manifestPath)

	f, err := os.Open(manifestPath)
	require.NoError(t, err)

	defer f.Close()

	var m nixcacheindex.Manifest

	err = json.NewDecoder(f).Decode(&m)
	require.NoError(t, err)

	assert.Equal(t, int64(2), m.ItemCount)
	assert.Equal(t, 1, m.Version)

	// Verify URLs
	assert.Equal(t, "http://cache.example.com/nix-cache-index/journal/", m.Urls.JournalBase)
	assert.Equal(t, "http://cache.example.com/nix-cache-index/shards/", m.Urls.ShardsBase)
	assert.Equal(t, "http://cache.example.com/nix-cache-index/deltas/", m.Urls.DeltasBase)

	// 4. Verify Shards exist and are compressed
	shardsDir := filepath.Join(dir, "store", "nix-cache-index", "shards", "1")
	entries, err := os.ReadDir(shardsDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries)

	// Check for .zst extension
	found := false

	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".idx.zst") {
			found = true

			break
		}
	}

	assert.True(t, found, "Expected to find .idx.zst shard file")

	// 5. Test Query
	c.SetExperimentalCacheIndex(true)

	// Check that we can fetch the manifest via the cache interface
	rc, err := c.Fetch(ctx, nixcacheindex.ManifestPath)
	require.NoError(t, err)
	rc.Close()
}
