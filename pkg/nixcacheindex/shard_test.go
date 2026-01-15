package nixcacheindex_test

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

func TestShardReadWrite(t *testing.T) {
	t.Parallel()

	// Generate some sorted hashes
	var hashes []*big.Int

	start := big.NewInt(1000)
	for i := 0; i < 500; i++ {
		// Add some gaps
		h := new(big.Int).Add(start, big.NewInt(int64(i*10+(i%5)))) // deterministic gaps
		hashes = append(hashes, h)
	}

	params := nixcacheindex.Encoding{
		Parameter:  4, // k=4
		HashBits:   160,
		PrefixBits: 0, // Depth 0
	}

	var buf bytes.Buffer

	err := nixcacheindex.WriteShard(&buf, hashes, params)
	require.NoError(t, err)

	// Read back
	r := bytes.NewReader(buf.Bytes())
	sr, err := nixcacheindex.ReadShard(r)
	require.NoError(t, err)

	assert.Equal(t, uint64(500), sr.Header.ItemCount)
	assert.Equal(t, uint8(4), sr.Header.GolombK)

	// Sparse Index Count: 500 items. Intervals 256. 0, 256. -> 2 entries.
	assert.Equal(t, uint64(2), sr.Header.SparseIndexCount)

	// Verify Contains
	for _, h := range hashes {
		contains, err := sr.Contains(h)
		require.NoError(t, err)
		assert.True(t, contains, "Shard should contain %s", h)
	}

	// Verify Missing
	missing := big.NewInt(999)
	contains, err := sr.Contains(missing)
	require.NoError(t, err)
	assert.False(t, contains, "Shard should not contain %s", missing)

	missing2 := big.NewInt(1005) // In between 1000 and 1011 (gap 11)
	contains, err = sr.Contains(missing2)
	require.NoError(t, err)
	assert.False(t, contains)
}

func TestWriteShard_Empty(t *testing.T) {
	t.Parallel()

	err := nixcacheindex.WriteShard(&bytes.Buffer{}, nil, nixcacheindex.Encoding{})
	assert.Error(t, err)
}

func TestShardSparseAlignment(t *testing.T) {
	t.Parallel()

	// Create enough items to trigger sparse index ( > 256)
	count := 300

	hashes := make([]*big.Int, count)
	for i := 0; i < count; i++ {
		hashes[i] = big.NewInt(int64(i))
	}

	params := nixcacheindex.Encoding{Parameter: 8, HashBits: 160, PrefixBits: 0}

	var buf bytes.Buffer

	err := nixcacheindex.WriteShard(&buf, hashes, params)
	require.NoError(t, err)

	sr, err := nixcacheindex.ReadShard(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)

	// Check sparse index entries
	require.Len(t, sr.SparseIndex, 2)
	assert.Equal(t, 0, sr.SparseIndex[0].HashSuffix.Cmp(big.NewInt(0)), "Entry 0 should be 0")
	assert.Equal(t, 0, sr.SparseIndex[1].HashSuffix.Cmp(big.NewInt(256)), "Entry 1 should be 256")

	// Verify lookups around the boundary
	ok, _ := sr.Contains(big.NewInt(255))
	assert.True(t, ok)
	ok, _ = sr.Contains(big.NewInt(256))
	assert.True(t, ok)
	ok, _ = sr.Contains(big.NewInt(257))
	assert.True(t, ok)
}
