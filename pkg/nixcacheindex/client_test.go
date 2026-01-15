package nixcacheindex_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/big"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nixcacheindex"
)

// MockFetcher.
type MockFetcher struct {
	mock.Mock
	files      map[string][]byte
	fetchCalls int
}

func (m *MockFetcher) Fetch(_ context.Context, path string) (io.ReadCloser, error) {
	m.fetchCalls++
	if data, ok := m.files[path]; ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	return nil, fmt.Errorf("%w: %s", nixcacheindex.ErrShardNotFound, path)
}

func TestClientQuery_EndToEnd(t *testing.T) {
	t.Parallel()

	// Setup
	// 1. Manifest
	manifest := nixcacheindex.NewManifest()
	manifest.Sharding.Depth = 0 // Single root shard for simplicity
	manifest.Epoch.Current = 10
	manifest.Journal.CurrentSegment = 1000
	manifest.Journal.SegmentDurationSeconds = 100
	manifest.Journal.RetentionCount = 1
	// Use mock URLs
	manifest.Urls.JournalBase = "https://mock/journal/"
	manifest.Urls.ShardsBase = "https://mock/shards/"

	var manifestBuf bytes.Buffer
	require.NoError(t, manifest.Write(&manifestBuf))

	// 2. Journal
	// Current segment (1000). Contains +hashA, -hashB
	hashA := "0000000000000000000000000000000a" // Added
	hashB := "0000000000000000000000000000000b" // Deleted

	journalEntries := []nixcacheindex.JournalEntry{
		{Op: nixcacheindex.OpAdd, Hash: hashA},
		{Op: nixcacheindex.OpDelete, Hash: hashB},
	}

	var journalBuf bytes.Buffer
	require.NoError(t, nixcacheindex.WriteJournal(&journalBuf, journalEntries))

	// 3. Shard (Epoch 10, root)
	// Contains hashC
	hashC := "0000000000000000000000000000000c"
	bnC, _ := nixcacheindex.ParseHash(hashC)

	// Also contains hashB (which was deleted in journal, so journal should take precedence)
	bnB, _ := nixcacheindex.ParseHash(hashB)

	hashes := []*big.Int{bnB, bnC} // Sorted? B < C. Yes.

	var shardBuf bytes.Buffer

	params := nixcacheindex.Encoding{Parameter: 4, HashBits: 160, PrefixBits: 0}
	require.NoError(t, nixcacheindex.WriteShard(&shardBuf, hashes, params))

	// Compress the shard data
	var compressedShardBuf bytes.Buffer

	enc, err := zstd.NewWriter(&compressedShardBuf)
	require.NoError(t, err)
	_, err = enc.Write(shardBuf.Bytes())
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	// 4. Mock Files
	mockFiles := map[string][]byte{
		nixcacheindex.ManifestPath:            manifestBuf.Bytes(),
		"https://mock/journal/1000.log":       journalBuf.Bytes(),
		"https://mock/journal/900.log":        nil, // Previous segment empty/missing
		"https://mock/shards/10/root.idx.zst": compressedShardBuf.Bytes(),
	}

	fetcher := &MockFetcher{files: mockFiles}
	client := nixcacheindex.NewClient(context.Background(), fetcher)

	// Test Cases

	// Case 1: HashA (Added in Journal) -> ProbableHit
	res, err := client.Query(context.Background(), hashA)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.ProbableHit, res, "HashA should be ProbableHit")

	// Case 2: HashB (Deleted in Journal) -> DefiniteMiss
	// Even though it is in Shard!
	res, err = client.Query(context.Background(), hashB)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.DefiniteMiss, res, "HashB should be DefiniteMiss (deleted in journal)")

	// Case 3: HashC (In Shard, not in Journal) -> DefiniteHit
	res, err = client.Query(context.Background(), hashC)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.DefiniteHit, res, "HashC should be DefiniteHit")

	// Case 4: HashD (Missing) -> DefiniteMiss
	hashD := "0000000000000000000000000000000d"
	res, err = client.Query(context.Background(), hashD)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.DefiniteMiss, res, "HashD should be DefiniteMiss")
}

func TestClientQuery_Caching(t *testing.T) {
	t.Parallel()

	// Setup
	manifest := nixcacheindex.NewManifest()
	manifest.Sharding.Depth = 0
	manifest.Epoch.Current = 1
	manifest.Journal.RetentionCount = 0 // Minimize journal fetches
	manifest.Urls.ShardsBase = "https://mock/shards/"

	var manifestBuf bytes.Buffer
	require.NoError(t, manifest.Write(&manifestBuf))

	hashA := "0000000000000000000000000000000a"
	bnA, _ := nixcacheindex.ParseHash(hashA)
	hashes := []*big.Int{bnA}

	var shardBuf bytes.Buffer

	params := nixcacheindex.Encoding{Parameter: 4, HashBits: 160, PrefixBits: 0}
	require.NoError(t, nixcacheindex.WriteShard(&shardBuf, hashes, params))

	mockFiles := map[string][]byte{
		nixcacheindex.ManifestPath:           manifestBuf.Bytes(),
		"https://mock/shards/1/root.idx.zst": shardBuf.Bytes(),
	}
	// Note: client.go checks fmt.Sprintf("%s%d/%s.idx.zst", ...), so the path MUST end in .zst
	// even if we don't actually compress it in the mock for this test (zstd reader might error if not valid zstd)
	// Let's compress it to be safe.

	var compressedShardBuf bytes.Buffer

	enc, err := zstd.NewWriter(&compressedShardBuf)
	require.NoError(t, err)
	_, err = enc.Write(shardBuf.Bytes())
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	mockFiles["https://mock/shards/1/root.idx.zst"] = compressedShardBuf.Bytes()

	fetcher := &MockFetcher{files: mockFiles}
	client := nixcacheindex.NewClient(context.Background(), fetcher)

	// First query: should fetch shard
	res, err := client.Query(context.Background(), hashA)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.DefiniteHit, res)
	// 1 (manifest) + 1 (current journal) + 1 (shard) = 3
	assert.Equal(t, 3, fetcher.fetchCalls)

	// Second query for same hash: should use cache for shard
	res, err = client.Query(context.Background(), hashA)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.DefiniteHit, res)
	// No new manifest fetch (cached in Client.manifest if already loaded),
	// but Query currently re-fetches manifest if manifest is nil.
	// Wait, LoadManifest is only called if c.manifest == nil.
	// But it re-fetches journal segments every time.
	// So 3 + 1 (journal) = 4
	assert.Equal(t, 4, fetcher.fetchCalls, "Should not have fetched shard again")

	// Third query for different hash in same shard: should use cache for shard
	hashB := "0000000000000000000000000000000b"
	res, err = client.Query(context.Background(), hashB)
	require.NoError(t, err)
	assert.Equal(t, nixcacheindex.DefiniteMiss, res)
	// 4 + 1 (journal) = 5
	assert.Equal(t, 5, fetcher.fetchCalls, "Should not have fetched shard again")
}
