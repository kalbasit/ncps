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
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

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

	nf, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(f.ctx)
	require.NoError(t, err)

	ni, err := f.c.dbClient.Ent().NarInfo.Create().
		SetHash(narInfoHash).
		SetURL(narURL.String()).
		Save(f.ctx)
	require.NoError(t, err)

	// Link the narinfo to the nar_file via narinfo_nar_files (the relation the GC
	// resolves through), mirroring how storeInDatabase wires them.
	require.NoError(t, f.c.dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(ni.ID).
		SetNarFileID(nf.ID).
		Exec(f.ctx))

	if old {
		_, err = f.db.DB().ExecContext(f.ctx,
			"UPDATE nar_files SET created_at = ? WHERE hash = ?",
			time.Now().Add(-10*time.Minute), narHash)
		require.NoError(t, err)
	}
}

// seedBackingLessRowMulti inserts one placeholder nar_file (no whole-file in store)
// linked to several narinfos — several store paths sharing one NAR — aged past the
// recovery cutoff so the sweep considers it.
func (f *gcTestFixture) seedBackingLessRowMulti(t *testing.T, narHash string, narInfoHashes []string) {
	t.Helper()

	narURL := nar.URL{Hash: narHash, Compression: nar.CompressionTypeNone}

	nf, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(f.ctx)
	require.NoError(t, err)

	for _, narInfoHash := range narInfoHashes {
		ni, err := f.c.dbClient.Ent().NarInfo.Create().
			SetHash(narInfoHash).
			SetURL(narURL.String()).
			Save(f.ctx)
		require.NoError(t, err)

		require.NoError(t, f.c.dbClient.Ent().NarInfoNarFile.Create().
			SetNarinfoID(ni.ID).
			SetNarFileID(nf.ID).
			Exec(f.ctx))
	}

	_, err = f.db.DB().ExecContext(f.ctx,
		"UPDATE nar_files SET created_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), nf.ID)
	require.NoError(t, err)
}

// seedBackingLessRowNoNarInfo inserts a placeholder nar_file with NO linked narinfo,
// optionally aged past the recovery cutoff.
func (f *gcTestFixture) seedBackingLessRowNoNarInfo(t *testing.T, narHash string, old bool) {
	t.Helper()

	_, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(f.ctx)
	require.NoError(t, err)

	if old {
		_, err = f.db.DB().ExecContext(f.ctx,
			"UPDATE nar_files SET created_at = ? WHERE hash = ?",
			time.Now().Add(-10*time.Minute), narHash)
		require.NoError(t, err)
	}
}

// gcHash pads seed to a 32-character store-path hash for test rows.
func gcHash(seed string) string {
	const want = 32
	if len(seed) >= want {
		return seed[:want]
	}

	return seed + strings.Repeat("0", want-len(seed))
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

func (f *gcTestFixture) narInfoExists(t *testing.T, narInfoHash string) bool {
	t.Helper()

	exists, err := f.c.dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(narInfoHash)).Exist(f.ctx)
	require.NoError(t, err)

	return exists
}

func (f *gcTestFixture) narFileID(t *testing.T, narHash string) int {
	t.Helper()

	id, err := f.c.dbClient.Ent().NarFile.Query().Where(entnarfile.HashEQ(narHash)).OnlyID(f.ctx)
	require.NoError(t, err)

	return id
}

func (f *gcTestFixture) narInfoID(t *testing.T, narInfoHash string) int {
	t.Helper()

	id, err := f.c.dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(narInfoHash)).OnlyID(f.ctx)
	require.NoError(t, err)

	return id
}

// linkNarInfoToNewNarFile creates a second, distinct nar_file variant and links the
// existing narinfo to it as well — making the narinfo M:N-linked to two nar_files.
func (f *gcTestFixture) linkNarInfoToNewNarFile(t *testing.T, narInfoHash, narFileHash string) {
	t.Helper()

	nf, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narFileHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(2345).
		Save(f.ctx)
	require.NoError(t, err)

	require.NoError(t, f.c.dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(f.narInfoID(t, narInfoHash)).
		SetNarFileID(nf.ID).
		Exec(f.ctx))
}

// TestRecoveryGCKeepsNarInfoLinkedToAnotherNarFile guards the M:N relationship: a
// verified narinfo that is ALSO linked to a different nar_file variant must NOT be
// deleted, since deleting it cascades away the other link and orphans that still-backed
// row. GC may unlink/GC the backing-less row, but the shared narinfo and the other
// nar_file survive.
func TestRecoveryGCKeepsNarInfoLinkedToAnotherNarFile(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "9lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	const otherNarHash = "alid9xrpirkzcpqsxfq02qwiq0yd70ch"

	shared := gcHash("gcshared1")

	f.seedBackingLessRowMulti(t, narHash, []string{shared})
	f.linkNarInfoToNewNarFile(t, shared, otherNarHash)

	_, err := f.c.gcDeleteAbsentNarInfosAndMaybeNarFile(
		f.ctx, f.narFileID(t, narHash), []int{f.narInfoID(t, shared)},
	)
	require.NoError(t, err)

	assert.True(t, f.narInfoExists(t, shared),
		"a narinfo still linked to another nar_file must NOT be deleted")
	assert.True(t, f.narFileExists(t, otherNarHash),
		"the other nar_file variant must remain (its narinfo must not be cascade-orphaned)")
}

// TestRecoveryGCKeepsNarFileWhenUnverifiedNarInfoRemains is the race guard: if a
// narinfo links to the nar_file but is NOT in the verified-absent set (e.g. linked by
// a concurrent storeInDatabase/repair after the GC snapshotted and probed upstreams),
// the GC deletes only the verified narinfos and KEEPS the nar_file, so the unverified
// narinfo is never cascade-orphaned into a fresh dangling row.
func TestRecoveryGCKeepsNarFileWhenUnverifiedNarInfoRemains(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "7lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	verified := gcHash("gcverified1")
	concurrent := gcHash("gcconcurrent1")

	f.seedBackingLessRowMulti(t, narHash, []string{verified, concurrent})

	// Only `verified` was confirmed genuinely absent upstream; `concurrent` stands in
	// for a narinfo linked after the verified snapshot and is omitted from the set.
	deleted, err := f.c.gcDeleteAbsentNarInfosAndMaybeNarFile(
		f.ctx, f.narFileID(t, narHash), []int{f.narInfoID(t, verified)},
	)
	require.NoError(t, err)

	assert.False(t, deleted,
		"nar_file must be kept while an unverified narinfo still links to it")
	assert.False(t, f.narInfoExists(t, verified),
		"the verified-absent narinfo must be deleted")
	assert.True(t, f.narInfoExists(t, concurrent),
		"the unverified (concurrently-linked) narinfo must NOT be orphaned or deleted")
	assert.True(t, f.narFileExists(t, narHash),
		"the nar_file must survive so the unverified narinfo stays linked, not dangling")
}

// TestRecoveryGCDeletesNarFileWhenAllVerified exercises the helper's all-verified path:
// when every linked narinfo is in the verified set, both the narinfos and the nar_file
// are deleted and the helper reports the nar_file as deleted.
func TestRecoveryGCDeletesNarFileWhenAllVerified(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "8lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	a := gcHash("gcallverified1")
	b := gcHash("gcallverified2")

	f.seedBackingLessRowMulti(t, narHash, []string{a, b})

	deleted, err := f.c.gcDeleteAbsentNarInfosAndMaybeNarFile(
		f.ctx, f.narFileID(t, narHash), []int{f.narInfoID(t, a), f.narInfoID(t, b)},
	)
	require.NoError(t, err)

	assert.True(t, deleted, "nar_file must be deleted when every linked narinfo is verified-absent")
	assert.False(t, f.narInfoExists(t, a), "verified-absent narinfo must be deleted")
	assert.False(t, f.narInfoExists(t, b), "verified-absent narinfo must be deleted")
	assert.False(t, f.narFileExists(t, narHash), "nar_file must be deleted when no links remain")
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
	assert.False(t, f.narInfoExists(t, absentNarInfoHash),
		"the narinfo of a genuinely-absent placeholder must be deleted, not left dangling")
}

// TestRecoveryGCDeletesAllLinkedNarInfosWhenGenuinelyAbsent verifies that when several
// store paths share one genuinely-absent backing-less NAR, GC deletes the nar_file AND
// every linked narinfo, leaving none dangling.
func TestRecoveryGCDeletesAllLinkedNarInfosWhenGenuinelyAbsent(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "5lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	// None of these narinfo hashes are served by the testdata upstream → all 404 → absent.
	narInfoHashes := []string{
		gcHash("gcmultiabsent1"),
		gcHash("gcmultiabsent2"),
		gcHash("gcmultiabsent3"),
	}

	f.seedBackingLessRowMulti(t, narHash, narInfoHashes)
	require.True(t, f.narFileExists(t, narHash))

	f.runRecovery(t)

	assert.False(t, f.narFileExists(t, narHash),
		"a genuinely-absent backing-less placeholder must be garbage-collected")

	for _, narInfoHash := range narInfoHashes {
		assert.False(t, f.narInfoExists(t, narInfoHash),
			"every narinfo linked to a genuinely-absent placeholder must be deleted")
	}
}

// TestRecoveryGCDeletesOrphanPlaceholderWithoutNarInfo verifies the unchanged
// zero-linked-narinfo branch: a backing-less placeholder with no linked narinfo is
// still garbage-collected.
func TestRecoveryGCDeletesOrphanPlaceholderWithoutNarInfo(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "6lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	f.seedBackingLessRowNoNarInfo(t, narHash, true)
	require.True(t, f.narFileExists(t, narHash))

	f.runRecovery(t)

	assert.False(t, f.narFileExists(t, narHash),
		"a backing-less placeholder with no linked narinfo must be garbage-collected")
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
	assert.True(t, f.narInfoExists(t, testdata.Nar1.NarInfoHash),
		"the narinfo must NOT be deleted while it is still present upstream")
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
	assert.True(t, f.narInfoExists(t, narInfoHash),
		"the narinfo must NOT be deleted on a transient/unknown upstream probe")
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
