package cache

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"

	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// gcTestFixture wires a CDC cache to a testdata upstream server for the
// placeholder-GC recovery tests.
type gcTestFixture struct {
	c   *Cache
	db  *database.Client
	ts  *testdata.Server
	ctx context.Context
}

func newGCTestFixture(t *testing.T) *gcTestFixture {
	t.Helper()

	c, db, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	ts := testdata.NewTestServer(t, 40)
	t.Cleanup(ts.Close)

	uc, err := upstream.New(newContext(), testhelper.MustParseURL(t, ts.URL), &upstream.Options{
		PublicKeys: testdata.PublicKeys(),
	})
	require.NoError(t, err)

	c.AddUpstreamCaches(newContext(), uc)
	<-c.GetHealthChecker().Trigger()

	return &gcTestFixture{c: c, db: db, ts: ts, ctx: context.Background()}
}

// seedBackingLessRow inserts a placeholder nar_file row (no whole-file in store) plus
// a narinfo linking the NAR URL to narInfoHash, optionally aged past the recovery
// cutoff. It returns the NAR hash used for the nar_file row.
func (f *gcTestFixture) seedBackingLessRow(t *testing.T, narHash, narInfoHash string, old bool) {
	t.Helper()

	narURL := nar.URL{Hash: narHash, Compression: nar.CompressionTypeNone}

	_, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(f.ctx)
	require.NoError(t, err)

	_, err = f.c.dbClient.Ent().NarInfo.Create().
		SetHash(narInfoHash).
		SetURL(narURL.String()).
		Save(f.ctx)
	require.NoError(t, err)

	if old {
		_, err = f.db.DB().ExecContext(f.ctx,
			"UPDATE nar_files SET created_at = ? WHERE hash = ?",
			time.Now().Add(-10*time.Minute), narHash)
		require.NoError(t, err)
	}
}

func (f *gcTestFixture) runRecovery(t *testing.T) {
	t.Helper()

	schedule, err := cron.ParseStandard("@every 5m")
	require.NoError(t, err)

	f.c.runCDCLazyRecovery(f.ctx, schedule, 10)()
}

func (f *gcTestFixture) narFileExists(t *testing.T, narHash string) bool {
	t.Helper()

	exists, err := f.c.dbClient.Ent().NarFile.Query().Where(entnarfile.HashEQ(narHash)).Exist(f.ctx)
	require.NoError(t, err)

	return exists
}

// TestRecoveryGCDeletesGenuinelyAbsentPlaceholder verifies that a backing-less
// placeholder whose narinfo is a definitive 404 on every healthy upstream is deleted.
func TestRecoveryGCDeletesGenuinelyAbsentPlaceholder(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	// narInfoHash is not served by the testdata upstream, so a HEAD returns 404.
	const narHash = "1lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	const absentNarInfoHash = "gcabsent00000000000000000000000a"

	f.seedBackingLessRow(t, narHash, absentNarInfoHash, true)
	require.True(t, f.narFileExists(t, narHash))

	f.runRecovery(t)

	assert.False(t, f.narFileExists(t, narHash),
		"a genuinely-absent backing-less placeholder must be garbage-collected")
}

// TestRecoveryGCKeepsPlaceholderPresentUpstream verifies that a placeholder whose
// narinfo still exists upstream is NOT deleted (GetNar can still recover it).
func TestRecoveryGCKeepsPlaceholderPresentUpstream(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "2lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	// Nar1's narinfo IS served by the testdata upstream → HEAD 200 → present.
	f.seedBackingLessRow(t, narHash, testdata.Nar1.NarInfoHash, true)

	f.runRecovery(t)

	assert.True(t, f.narFileExists(t, narHash),
		"a placeholder whose NAR is still available upstream must NOT be deleted")
}

// TestRecoveryGCKeepsPlaceholderOnTransientProbe verifies that an inconclusive
// (transient) upstream probe never deletes the placeholder.
func TestRecoveryGCKeepsPlaceholderOnTransientProbe(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	// Make every narinfo HEAD return 503 (treated as unknown, not absent).
	idx := f.ts.AddMaybeHandler(func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, ".narinfo") {
			w.WriteHeader(http.StatusServiceUnavailable)

			return true
		}

		return false
	})

	t.Cleanup(func() { f.ts.RemoveMaybeHandler(idx) })

	const narHash = "3lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	const narInfoHash = "gctransient0000000000000000000aa"

	f.seedBackingLessRow(t, narHash, narInfoHash, true)

	f.runRecovery(t)

	assert.True(t, f.narFileExists(t, narHash),
		"a transient/unknown upstream probe must NOT delete the placeholder")
}

// TestRecoveryGCSkipsFreshPlaceholder verifies that a backing-less placeholder newer
// than the recovery cutoff is never considered (so an in-flight download's placeholder
// is not raced), even if its narinfo would be a definitive 404.
func TestRecoveryGCSkipsFreshPlaceholder(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "4lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	const absentNarInfoHash = "gcfresh000000000000000000000000a"

	// old=false → created_at defaults to now, outside the recovery cutoff.
	f.seedBackingLessRow(t, narHash, absentNarInfoHash, false)

	f.runRecovery(t)

	assert.True(t, f.narFileExists(t, narHash),
		"a fresh placeholder must not be garbage-collected, even if genuinely absent")
}
