package cache

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// xzCompress compresses data with xz and returns the compressed bytes.
func xzCompress(t *testing.T, data string) []byte {
	t.Helper()

	var buf bytes.Buffer

	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err)

	_, err = io.WriteString(xw, data)
	require.NoError(t, err)

	require.NoError(t, xw.Close())

	return buf.Bytes()
}

// TestCDCWholeFileServeReportsServedCompression reproduces the bug where an xz
// whole-file NAR served during the lazy-chunking transition window was
// mislabelled with Compression:none.
//
// During lazy chunking, MigrateNarToChunks creates a coexisting
// Compression:none chunked nar_file row while the original .nar.xz whole file
// (and its xz nar_file row) are retained. getNarFromStore serves the xz whole
// file (raw xz bytes, xz Content-Length), but getNarFileFromDB is CDC-first and
// returns the none row, so the response advertised Compression:none with an xz
// Content-Length (< NarSize). nix then failed with "NAR is incomplete" /
// "input compression not recognized". The fix makes the whole-file serve path
// report the compression of the bytes it ACTUALLY serves.
func TestCDCWholeFileServeReportsServedCompression(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	// Build a real xz NAR whose decompressed length is known. We do NOT use the
	// testdata NarText for the bytes because that is random (not valid xz); we
	// only borrow the hash/query so the storage key and DB lookups line up.
	const decompressedLen = 50160

	original := testhelper.MustRandString(decompressedLen)
	xzBytes := xzCompress(t, original)

	entry := testdata.Nar1
	xzURL := nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz}

	// 1. Store the xz whole-file NAR (CDC disabled at this point).
	require.NoError(t, c.PutNar(ctx, xzURL, io.NopCloser(bytes.NewReader(xzBytes))))

	// 2. Enable CDC with lazy chunking and a LONG delete-delay so the xz whole
	// file is NOT deleted after migration (reproduces coexistence).
	chunkStoreDir := filepath.Join(dir, "chunks-store")
	chunkStore, err := chunk.NewLocalStore(chunkStoreDir)
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
	c.SetCDCLazyChunking(true, 1)
	c.SetCDCDeleteDelay(time.Hour)

	// 3. Migrate the NAR to chunks. With lazy chunking enabled this creates the
	// coexisting Compression:none chunked row and leaves the xz whole file (and
	// its xz nar_file row) in place.
	require.NoError(t, c.MigrateNarToChunks(ctx, &xzURL))

	// The xz whole file must still exist on disk.
	assert.True(t, c.HasNarInStore(ctx, nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz}),
		"xz whole file must still exist on disk after lazy-chunking migration")

	// A chunked none row with total_chunks > 0 must coexist.
	noneRow, err := fetchNarFile(ctx, dbClient, entry.NarHash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err, "coexisting none nar_file row must exist after migration")
	assert.Positive(t, noneRow.TotalChunks, "none row must be fully chunked (total_chunks > 0)")

	// And the xz row must still exist (lazy chunking does not delete it).
	xzRow, err := fetchNarFile(ctx, dbClient, entry.NarHash, nar.CompressionTypeXz.String(), "")
	require.NoError(t, err, "xz nar_file row must still exist after lazy-chunking migration")
	assert.Equal(t, int64(0), xzRow.TotalChunks, "xz row is a whole-file row (total_chunks == 0)")

	// 4. Request the xz NAR. The whole-file serve path must advertise xz (the
	// compression of the bytes it actually streams), NOT the CDC-first none row.
	nu, size, rc, err := c.GetNar(ctx, nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeXz})
	require.NoError(t, err)

	t.Cleanup(func() { _ = rc.Close() })

	// Before the fix: nu.Compression == none (mislabelled).
	assert.Equal(t, nar.CompressionTypeXz, nu.Compression,
		"GetNar must report the compression of the served bytes (xz), not the CDC-first none row")

	// The streamed bytes, decompressed per the returned compression, must total
	// exactly NarSize. Before the fix this is impossible: the bytes are xz but
	// the response labels them none (so they would be treated as already
	// uncompressed and total the xz size, < NarSize).
	served, err := io.ReadAll(rc)
	require.NoError(t, err)

	// size is the on-disk (compressed) size for a whole-file xz serve.
	assert.Equal(t, int64(len(served)), size, "returned size must match the streamed byte count")

	dec, err := nar.DecompressReader(ctx, bytes.NewReader(served), nu.Compression)
	require.NoError(t, err)

	defer dec.Close()

	decompressed, err := io.ReadAll(dec)
	require.NoError(t, err)
	assert.Len(t, decompressed, decompressedLen,
		"served bytes, decompressed per the returned compression, must total NarSize")
}
