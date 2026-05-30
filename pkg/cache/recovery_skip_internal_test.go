package cache

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	locklocal "github.com/kalbasit/ncps/pkg/lock/local"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/pkg/storage/local"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// countingNarStore wraps a NarStore and records how many times GetNar is called
// per NAR hash, so a test can observe whether the recovery job attempted to read a
// NAR's bytes from the store (i.e. tried to migrate it).
type countingNarStore struct {
	storage.NarStore

	mu       sync.Mutex
	getCalls map[string]int
}

func (s *countingNarStore) GetNar(ctx context.Context, narURL nar.URL) (int64, io.ReadCloser, error) {
	s.mu.Lock()
	if s.getCalls == nil {
		s.getCalls = make(map[string]int)
	}

	s.getCalls[narURL.Hash]++
	s.mu.Unlock()

	return s.NarStore.GetNar(ctx, narURL)
}

func (s *countingNarStore) count(hash string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.getCalls[hash]
}

// TestRunCDCLazyRecoverySkipsBackingLessRows verifies that the CDC lazy-recovery
// job only re-drives stuck rows that actually have a whole-file in the store (which
// BackgroundMigrateNarToChunks can chunk). A backing-less stuck row (placeholder /
// genuinely-absent NAR) must NOT be re-driven: migration cannot help it, and doing
// so every interval is the production "error fetching nar from store: not found"
// spam that indefinitely retries a hash upstream genuinely does not have.
func TestRunCDCLazyRecoverySkipsBackingLessRows(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx := context.Background()

	dir := t.TempDir()
	dbFile := filepath.Join(dir, "var", "ncps", "db", "db.sqlite")
	testhelper.CreateMigrateDatabase(t, dbFile)

	dbClient, err := database.Open("sqlite:"+dbFile, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dbClient.Close() })

	localStore, err := local.New(newContext(), dir)
	require.NoError(t, err)

	spy := &countingNarStore{NarStore: localStore}

	c, err := New(newContext(), cacheName, dbClient, localStore, localStore, spy, "",
		locklocal.NewLocker(), locklocal.NewRWLocker(), downloadLockTTL, downloadPollTimeout, cacheLockTTL)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)

	// A stuck row WITH a whole-file in the store: recovery can (and must) re-drive it.
	present := testdata.Nar1
	presentURL := nar.URL{Hash: present.NarHash, Compression: present.NarCompression}
	require.NoError(t, c.PutNar(ctx, presentURL, io.NopCloser(strings.NewReader(present.NarText))))

	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))
	c.SetCDCLazyChunking(true, 1)

	// A stuck row with NO whole-file in the store (backing-less placeholder).
	absentHash := testdata.Nar2.NarHash
	_, err = c.dbClient.Ent().NarFile.Create().
		SetHash(absentHash).
		SetCompression(nar.CompressionTypeXz.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(ctx)
	require.NoError(t, err)

	// Mark both rows stuck (total_chunks=0, chunking_started_at=NULL) and old enough
	// to fall outside the recovery cutoff.
	old := time.Now().Add(-10 * time.Minute)
	_, err = dbClient.DB().ExecContext(ctx,
		"UPDATE nar_files SET total_chunks = 0, chunking_started_at = NULL, created_at = ?", old)
	require.NoError(t, err)

	schedule, err := cron.ParseStandard("@every 5m")
	require.NoError(t, err)

	c.runCDCLazyRecovery(ctx, schedule, 10)()

	// Recovery must re-drive the store-present stuck row (reads its bytes to chunk it).
	require.Eventually(t, func() bool { return spy.count(present.NarHash) >= 1 },
		5*time.Second, 10*time.Millisecond,
		"recovery should re-drive the store-present stuck row")

	// By the time the present row has been read, the (fast-failing) backing-less
	// migration would also have read the store if it had been triggered. It must not
	// have been: a backing-less row is skipped, never re-driven.
	assert.Equal(t, 0, spy.count(absentHash),
		"recovery must skip a backing-less stuck row, not re-drive it")
}
