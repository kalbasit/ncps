package ncps

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/config"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/testhelper"
)

// newCDCModeTestDB returns a freshly-migrated in-process SQLite client for
// exercising detectFsckCDCMode against real DB state.
func newCDCModeTestDB(t *testing.T) *database.Client {
	t.Helper()

	dbFile := filepath.Join(t.TempDir(), "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbClient.Close() })

	return dbClient
}

func addOrphanChunk(ctx context.Context, t *testing.T, dbClient *database.Client, hash string) {
	t.Helper()

	_, err := dbClient.Ent().Chunk.Create().
		SetHash(hash).
		SetSize(10).
		SetCompressedSize(5).
		Save(ctx)
	require.NoError(t, err)
}

// TestDetectFsckCDCMode_ChunkResidueOnly is the post-drain scenario: no
// cdc_enabled config and no chunked nar_files, but orphaned chunk rows remain.
// Detection MUST still enable CDC mode so the residue is reclaimable.
func TestDetectFsckCDCMode_ChunkResidueOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbClient := newCDCModeTestDB(t)

	addOrphanChunk(ctx, t, dbClient, "orphanchunkhashaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	reason := detectFsckCDCMode(ctx, dbClient, zerolog.Nop())

	require.Equal(t, cdcModeFromChunkResidue, reason)
	require.True(t, reason.enabled())
}

func setCDCEnabled(ctx context.Context, t *testing.T, dbClient *database.Client) {
	t.Helper()

	_, err := dbClient.Ent().ConfigEntry.Create().
		SetKey(config.KeyCDCEnabled).
		SetValue("true").
		Save(ctx)
	require.NoError(t, err)
}

func addChunkedNarFile(ctx context.Context, t *testing.T, dbClient *database.Client, hash string) {
	t.Helper()

	_, err := dbClient.Ent().NarFile.Create().
		SetHash(hash).
		SetCompression("xz").
		SetQuery("").
		SetFileSize(100).
		SetTotalChunks(2).
		Save(ctx)
	require.NoError(t, err)
}

// TestDetectFsckCDCMode_NilClient: a nil database client yields non-CDC mode
// rather than panicking.
func TestDetectFsckCDCMode_NilClient(t *testing.T) {
	t.Parallel()

	reason := detectFsckCDCMode(context.Background(), nil, zerolog.Nop())

	require.Equal(t, cdcModeOff, reason)
	require.False(t, reason.enabled())
}

// TestDetectFsckCDCMode_NeverCDC: a cache that never used CDC (no config key, no
// chunked nar_files, empty chunks table) MUST stay in non-CDC mode.
func TestDetectFsckCDCMode_NeverCDC(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbClient := newCDCModeTestDB(t)

	reason := detectFsckCDCMode(ctx, dbClient, zerolog.Nop())

	require.Equal(t, cdcModeOff, reason)
	require.False(t, reason.enabled())
}

// TestDetectFsckCDCMode_ConfigTakesPrecedence: cdc_enabled=true wins over the
// residue signal, so the residue reason is not reported even when chunks exist.
func TestDetectFsckCDCMode_ConfigTakesPrecedence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbClient := newCDCModeTestDB(t)

	setCDCEnabled(ctx, t, dbClient)
	addOrphanChunk(ctx, t, dbClient, "orphanchunkhashbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	reason := detectFsckCDCMode(ctx, dbClient, zerolog.Nop())

	require.Equal(t, cdcModeFromConfig, reason)
}

// TestDetectFsckCDCMode_ChunkedNarFiles: with no config key but a chunked
// nar_file present, the data-based fallback enables CDC mode.
func TestDetectFsckCDCMode_ChunkedNarFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbClient := newCDCModeTestDB(t)

	addChunkedNarFile(ctx, t, dbClient, "chunkednarfilehashcccccccccccccccccccccccccccccccccc")

	reason := detectFsckCDCMode(ctx, dbClient, zerolog.Nop())

	require.Equal(t, cdcModeFromChunkedNarFiles, reason)
}
