package cache

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testhelper"
)

// migrationRaceNarStore wraps a real local store and, for a chosen NAR hash,
// reports the whole file as present (HasNar -> true) but reports it as absent
// when actually read (GetNar -> ErrNotFound). This models the TOCTOU window in
// the read path: a concurrent background NAR->chunks migration deletes the
// whole-file NAR between serveNarFromStorageViaPipe's total_chunks check and its
// store read. The first GetNar for failHash invokes onFirstGet, used to flip the
// DB into its post-migration state (total_chunks committed), exactly as the real
// migration does immediately before deleting the file.
type migrationRaceNarStore struct {
	*local.Store

	failHash   string
	onFirstGet func()
	once       sync.Once
}

func (s *migrationRaceNarStore) HasNar(ctx context.Context, narURL nar.URL) bool {
	if narURL.Hash == s.failHash {
		return true
	}

	return s.Store.HasNar(ctx, narURL)
}

func (s *migrationRaceNarStore) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	if narURL.Hash == s.failHash {
		s.once.Do(func() {
			if s.onFirstGet != nil {
				s.onFirstGet()
			}
		})

		return 0, nil, storage.ErrNotFound
	}

	return s.Store.GetNar(ctx, narURL)
}

// raceTestCache builds a Cache whose narStore is a migrationRaceNarStore for the
// given hash, with CDC enabled and a chunk store initialized.
func raceTestCache(t *testing.T, hash string) (*Cache, *database.Client, *migrationRaceNarStore) {
	t.Helper()

	ctx := newContext()

	dir, err := os.MkdirTemp("", "cache-path-")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })

	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbClient.Close() })

	localStore, err := local.New(ctx, dir)
	require.NoError(t, err)

	narStore := &migrationRaceNarStore{Store: localStore, failHash: hash}

	c, err := New(ctx, cacheName, dbClient, localStore, localStore, narStore, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)
	t.Cleanup(c.Close)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)

	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	return c, dbClient, narStore
}

// TestServeNarFromStorageRaceWithMigrationFallsBackToChunks reproduces the flaky
// failure observed in TestCDCBackends/.../Mixed_Mode
// ("error fetching the nar from the store: not found"). When the whole-file NAR
// is removed by a concurrent background migration after the serve path decided to
// read from the store (total_chunks still observed as 0) but before the actual
// store read, the cache MUST fall back to reassembling the NAR from chunks rather
// than surfacing a spurious not-found error.
func TestServeNarFromStorageRaceWithMigrationFallsBackToChunks(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	const hash = "00ji9synj1r6h6sjw27wwv8fw98myxsg92q5ma1pvrbmh451kc27"

	c, dbClient, narStore := raceTestCache(t, hash)

	// Store the NAR via CDC: this writes chunks + junction links and commits
	// total_chunks > 0. No whole file is written to the narStore.
	content := strings.Repeat("mixed-mode race content; ", 256)
	nu := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}
	require.NoError(t, c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content))))

	committed, err := dbClient.Ent().NarFile.Query().Where(entnarfile.HashEQ(hash)).Only(ctx)
	require.NoError(t, err)
	require.Positive(t, committed.TotalChunks, "NAR must be chunked for this test")

	totalChunks := committed.TotalChunks

	// Model the stale read: at the moment the serve path decides store-vs-chunks,
	// the migration has not yet committed, so total_chunks is observed as 0.
	_, err = dbClient.Ent().NarFile.Update().Where(entnarfile.HashEQ(hash)).SetTotalChunks(0).Save(ctx)
	require.NoError(t, err)

	// The migration completes exactly when the store read happens: total_chunks is
	// committed (>0) and the whole file is deleted (GetNar returns ErrNotFound).
	narStore.onFirstGet = func() {
		_, uerr := dbClient.Ent().NarFile.Update().Where(entnarfile.HashEQ(hash)).SetTotalChunks(totalChunks).Save(ctx)
		require.NoError(t, uerr)
	}

	// hasInStore=true: the serve path observed the whole file as present.
	size, rc, err := c.serveNarFromStorageViaPipe(ctx, &nu, true)
	require.NoError(t, err,
		"a whole-file store miss caused by a concurrent migration must fall back to chunks, not surface not-found")
	require.NotNil(t, rc)

	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, content, string(got))
	require.Equal(t, int64(len(content)), size)
}

// TestServeNarFromStorageRaceCompressedRequestStillNotFound asserts the chunk
// fallback does NOT engage for a compressed request. Chunks are stored
// uncompressed, so a missing whole file for a .nar.xz request must still resolve
// to ErrNotFound (the client then falls back to an upstream with the original
// compressed file) rather than serving raw chunk bytes.
func TestServeNarFromStorageRaceCompressedRequestStillNotFound(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	const hash = "1s8p1kgdms8rmxkq24q51wc7zpn0aqcwgzvc473v9cii7z2qyxq0"

	c, dbClient, _ := raceTestCache(t, hash)

	content := strings.Repeat("compressed request race content; ", 256)
	nu := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}
	require.NoError(t, c.PutNar(ctx, nu, io.NopCloser(strings.NewReader(content))))

	// Force the store branch for the (compressed) request.
	_, err := dbClient.Ent().NarFile.Update().Where(entnarfile.HashEQ(hash)).SetTotalChunks(0).Save(ctx)
	require.NoError(t, err)

	xzURL := nar.URL{Hash: hash, Compression: nar.CompressionTypeXz}

	_, _, err = c.serveNarFromStorageViaPipe(ctx, &xzURL, true)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"a compressed request whose whole file is gone must not fall back to uncompressed chunks")
}

// TestServeNarFromStorageRaceGenuinelyAbsentReturnsNotFound asserts that when the
// whole file is gone AND there are no chunks, the fallback preserves not-found
// semantics rather than masking a genuinely absent NAR.
func TestServeNarFromStorageRaceGenuinelyAbsentReturnsNotFound(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	const hash = "abcdghij2k3l4m5n6o7p8q9r0s1t2u3v4w5x6y7z8a9b0c1d2e3f"

	c, _, _ := raceTestCache(t, hash)

	// No PutNar: neither a whole file nor chunks exist for this hash. The narStore
	// wrapper still reports the whole file as present at check time but absent on
	// read, exercising the fallback with nothing to fall back to.
	nu := nar.URL{Hash: hash, Compression: nar.CompressionTypeNone}

	_, _, err := c.serveNarFromStorageViaPipe(ctx, &nu, true)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"a genuinely absent NAR (no whole file, no chunks) must still return not-found")
}
