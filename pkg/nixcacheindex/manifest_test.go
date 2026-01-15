package nixcacheindex_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

func TestManifestSerialization(t *testing.T) {
	t.Parallel()

	// RFC Example (approximate)
	jsonStr := `{
  "version": 1,
  "format": "hlssi",
  "created_at": "2026-01-13T12:00:00Z",
  "item_count": 1200000000,
  "sharding": {
    "depth": 2,
    "alphabet": "0123456789abcdfghijklmnpqrsvwxyz"
  },
  "encoding": {
    "type": "golomb-rice",
    "parameter": 8,
    "hash_bits": 160,
    "prefix_bits": 10
  },
  "urls": {
    "journal_base": "https://cache.example.com/nix-cache-index/journal/",
    "shards_base": "https://cache.example.com/nix-cache-index/shards/",
    "deltas_base": "https://cache.example.com/nix-cache-index/deltas/"
  },
  "journal": {
    "current_segment": 1705147200,
    "segment_duration_seconds": 300,
    "retention_count": 12
  },
  "epoch": {
    "current": 42,
    "previous": 41
  },
  "deltas": {
    "enabled": true,
    "oldest_base": 35,
    "compression": "zstd"
  }
}`

	m, err := nixcacheindex.LoadManifest(strings.NewReader(jsonStr))
	require.NoError(t, err)

	assert.Equal(t, 1, m.Version)
	assert.Equal(t, "hlssi", m.Format)
	assert.Equal(t, int64(1200000000), m.ItemCount)
	assert.Equal(t, 2, m.Sharding.Depth)
	assert.Equal(t, "golomb-rice", m.Encoding.Type)
	assert.Equal(t, "https://cache.example.com/nix-cache-index/journal/", m.Urls.JournalBase)
	assert.Equal(t, 42, int(m.Epoch.Current)) // Cast to int for comparison convenience if needed
	assert.Equal(t, 41, int(m.Epoch.Previous))

	// Test Serialization
	var buf bytes.Buffer

	err = m.Write(&buf)
	require.NoError(t, err)

	// Read back
	m2, err := nixcacheindex.LoadManifest(&buf)
	require.NoError(t, err)
	assert.Equal(t, m, m2)
}

func TestNewManifest(t *testing.T) {
	t.Parallel()

	m := nixcacheindex.NewManifest()
	assert.Equal(t, 1, m.Version)
	assert.Equal(t, "hlssi", m.Format)
	assert.Positive(t, m.CreatedAt.Unix())
	assert.NotEmpty(t, m.Urls.JournalBase)
}
