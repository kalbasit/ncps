package cache_test

import (
	"context"
	"crypto/sha256"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/nixhash"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarfilechunk "github.com/kalbasit/ncps/ent/narfilechunk"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
)

// chunkedNarFixture stores a NAR whole-file + its narinfo (creating the
// narinfo<->nar_file link), sets the narinfo NarHash to the actual content's
// hash (testdata's literal NarHash does not match its random NarText), enables
// CDC, and migrates the NAR to chunks. It returns the none-compression URL of
// the now-chunked NAR — the input to the reverse migration.
func chunkedNarFixture(
	ctx context.Context, t *testing.T, c *cache.Cache, dbClient *database.Client, dir string,
) (nar.URL, string) {
	t.Helper()

	entry := testdata.Nar1
	narURL := nar.URL{Hash: entry.NarHash, Compression: entry.NarCompression}

	require.NoError(t, c.PutNar(ctx, narURL, io.NopCloser(strings.NewReader(entry.NarText))))
	require.NoError(t, c.PutNarInfo(ctx, entry.NarInfoHash, io.NopCloser(strings.NewReader(entry.NarInfoText))))

	// Record the true NAR hash on the narinfo so content verification has a real
	// reference (the chunks store exactly entry.NarText).
	sum := sha256.Sum256([]byte(entry.NarText))
	narHash := nixhash.MustNewHashWithEncoding(nixhash.SHA256, sum[:], nixhash.NixBase32, true).String()

	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(entry.NarInfoHash)).
		SetNarHash(narHash).
		Save(ctx)
	require.NoError(t, err)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
	require.NoError(t, c.MigrateNarToChunks(ctx, &narURL))

	return nar.URL{Hash: entry.NarHash, Compression: nar.CompressionTypeNone}, entry.NarText
}

// TestMigrateChunksToNar_ReconstructsVerifiesAndStoresWholeFile is the slice-1
// tracer bullet: a chunked NAR is reconstructed, its assembled SHA-256 verified
// against the linked narinfo NarHash, and the whole file written to the store.
func TestMigrateChunksToNar_ReconstructsVerifiesAndStoresWholeFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, content := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// Sanity: the whole file is not in the store yet (only chunks back the NAR).
	require.False(t, c.HasNarInStore(ctx, noneURL),
		"precondition: chunked NAR should have no whole file in the store")

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL))

	assert.True(t, c.HasNarInStore(ctx, noneURL),
		"the whole NAR must be present in the store after de-chunking")

	// And it must serve the original content (proving reconstruction was correct).
	_, _, rc, err := c.GetNar(ctx, noneURL)
	require.NoError(t, err)

	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
}

// TestMigrateChunksToNar_FlipsRecordToWholeFile (slice 2): the nar_file is
// flipped to the whole-file representation (total_chunks=0, no chunk links).
func TestMigrateChunksToNar_FlipsRecordToWholeFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL))

	nf, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash), entnarfile.CompressionEQ(nar.CompressionTypeNone.String())).
		Only(ctx)
	require.NoError(t, err)
	assert.Zero(t, nf.TotalChunks, "nar_file must be flipped to whole-file (total_chunks=0)")

	linkCount, err := dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileID(nf.ID)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, linkCount, "chunk links must be removed after de-chunking")
}

// TestMigrateChunksToNar_ReclaimsOrphanedChunks (slice 3): chunks referenced
// only by the migrated NAR are reclaimed.
func TestMigrateChunksToNar_ReclaimsOrphanedChunks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	before, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	require.Positive(t, before, "fixture should have created chunks")

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL))

	after, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, after, "orphaned chunks must be reclaimed after de-chunking the only referencing NAR")
}

// TestMigrateChunksToNar_RetainsSharedChunks (slice 3): chunks still referenced
// by another nar_file are NOT deleted (dedup-safe reclamation).
func TestMigrateChunksToNar_RetainsSharedChunks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	nf1, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash), entnarfile.CompressionEQ(nar.CompressionTypeNone.String())).
		Only(ctx)
	require.NoError(t, err)

	links, err := dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileID(nf1.ID)).
		All(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, links)

	// A second nar_file referencing the SAME chunks (simulates cross-NAR dedup).
	nf2, err := dbClient.Ent().NarFile.Create().
		SetHash("sharedother0000000000000000000000000000000000000000000").
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(nf1.FileSize).
		SetTotalChunks(int64(len(links))).
		Save(ctx)
	require.NoError(t, err)

	for _, l := range links {
		_, err := dbClient.Ent().NarFileChunk.Create().
			SetNarFileID(nf2.ID).
			SetChunkID(l.ChunkID).
			SetChunkIndex(l.ChunkIndex).
			Save(ctx)
		require.NoError(t, err)
	}

	before, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL))

	after, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after, "chunks shared with another nar_file must be retained")
}
