package cache

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
)

// TestServeZstdRequestFromChunks reproduces the inverse compression desync
// (issue #1392) observed on a steady-state CDC deployment: the NAR is stored
// only as uncompressed CDC chunks, but the client's narinfo advertises
// `Compression: zstd`, so it requests `/nar/<hash>.nar.zst`. The serve path
// cannot produce a compressed stream from chunks and returns
// `does not exist in binary cache`.
//
// Correct behavior: reassemble the chunks and recompress to zstd on the fly,
// serving a zstd-labeled stream that decompresses to the original NAR.
func TestServeZstdRequestFromChunks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192)) // small sizes for testing

	content := "this is a test nar content that should be chunked by fastcdc and served as zstd"
	noneURL := nar.URL{Hash: "testnarzstd", Compression: nar.CompressionTypeNone}

	require.NoError(t, c.PutNar(ctx, noneURL, io.NopCloser(strings.NewReader(content))))

	// Precondition: the NAR exists only as chunks, no whole file in the store.
	require.False(t, c.HasNarInStore(ctx, noneURL),
		"precondition: the chunked NAR must not have a whole file in the store")

	// A client whose narinfo advertises zstd requests the compressed NAR.
	zstdURL := nar.URL{Hash: "testnarzstd", Compression: nar.CompressionTypeZstd}

	nu, _, rc, err := c.GetNar(ctx, zstdURL)
	require.NoError(t, err,
		"a zstd request for a chunked NAR must be served by recompression, not 404'd")

	t.Cleanup(func() { _ = rc.Close() })

	assert.Equal(t, nar.CompressionTypeZstd, nu.Compression,
		"the served stream must be labeled zstd")

	served, err := io.ReadAll(rc)
	require.NoError(t, err)

	dr, err := nar.DecompressReader(ctx, bytes.NewReader(served), nar.CompressionTypeZstd)
	require.NoError(t, err)

	defer dr.Close()

	got, err := io.ReadAll(dr)
	require.NoError(t, err)
	assert.Equal(t, content, string(got),
		"served zstd bytes must decompress to the original NAR")
}
