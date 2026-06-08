package cache

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
)

// putStagingParts splits content into partSize-byte part-objects and writes them
// for hash, returning the number of parts written.
func putStagingParts(t *testing.T, store storage.NarStore, hash, content string, partSize int) int64 {
	t.Helper()

	ctx := context.Background()

	var index int64

	for off := 0; off < len(content); off += partSize {
		end := off + partSize
		if end > len(content) {
			end = len(content)
		}

		_, err := store.PutStagingPart(ctx, hash, index, bytes.NewReader([]byte(content[off:end])), int64(end-off))
		require.NoError(t, err)

		index++
	}

	return index
}

// TestStagingPartReader_ReadsCompleteParts verifies that a reader tailing fully
// staged part-objects reassembles the complete, byte-correct NAR and then emits a
// clean EOF once the terminal complete marker is set.
func TestStagingPartReader_ReadsCompleteParts(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "abcabcabcabcabcabcabcabcabcabc12"
		content = "abcdefghijklmno" // 15 bytes -> 4 parts (4,4,4,3)
	)

	nParts := putStagingParts(t, store, hash, content, 4)

	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, nParts, ""))
	require.NoError(t, c.markStagingComplete(ctx, hash))

	r := c.newStagingPartReader(ctx, hash)
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err)

	assert.Equal(t, content, string(got))
}

// TestStagingPartReader_StallSurfacesError verifies that when the producer stalls
// — parts_available stops advancing and the complete marker never arrives — the
// reader surfaces a stream error after the per-part wait bound rather than a clean
// EOF at a truncated length (#660 / #1289 correctness contract).
func TestStagingPartReader_StallSurfacesError(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 4, true)

	ctx := context.Background()

	const (
		hash    = "deaddeaddeaddeaddeaddeaddeaddead"
		content = "abcdefgh" // 2 parts of 4, but the producer "stalls" after 1
	)

	putStagingParts(t, store, hash, content, 4)

	require.NoError(t, c.markStagingRequested(ctx, hash))
	// Only the first part is advertised; status stays "staging" (never complete).
	require.NoError(t, c.advanceStagingParts(ctx, hash, 1, ""))

	r := c.newStagingPartReader(ctx, hash)
	defer r.Close()

	// Short stall bound so the test does not wait the production default.
	r.maxWait = 200 * time.Millisecond
	r.pollEvery = 20 * time.Millisecond

	got, err := io.ReadAll(r)
	require.Error(t, err, "a stalled producer must surface a stream error")
	require.NotErrorIs(t, err, io.EOF)
	assert.Equal(t, content[:4], string(got), "bytes read before the stall are still delivered")
}

// TestServeNarFromStaging_Transcodes verifies that when the staged compression
// differs from the requested compression, the serve path transcodes on the fly
// (parity with the same-pod path) and advertises the compression actually served.
func TestServeNarFromStaging_Transcodes(t *testing.T) {
	t.Parallel()

	c, _, store, _, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	c.SetInflightStaging(true, 5*time.Minute, 1<<20, true)

	ctx := context.Background()

	const (
		hash      = "feedfeedfeedfeedfeedfeedfeedfeed"
		plaintext = "the quick brown fox jumps over the lazy dog"
	)

	// Stage the NAR in zstd; a reader requesting uncompressed must get plaintext.
	staged := CompressZstd(t, plaintext)
	nParts := putStagingParts(t, store, hash, staged, 1<<20)

	require.NoError(t, c.markStagingRequested(ctx, hash))
	require.NoError(t, c.advanceStagingParts(ctx, hash, nParts, nar.CompressionTypeZstd.String()))
	require.NoError(t, c.markStagingComplete(ctx, hash))

	narURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	size, reader, err := c.serveNarFromStaging(ctx, &narURL, hash, nar.CompressionTypeZstd)
	require.NoError(t, err)

	defer reader.Close()

	assert.Equal(t, int64(-1), size, "in-flight staging serves a streaming response of unknown length")
	assert.Equal(t, nar.CompressionTypeNone, narURL.Compression,
		"the served URL must advertise the compression actually served (decompressed)")

	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, plaintext, string(got))
}
