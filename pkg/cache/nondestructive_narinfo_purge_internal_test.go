package cache

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// narFileExists reports whether a nar_file row with the given hash is present.
func narFileExists(ctx context.Context, t *testing.T, c *Cache, hash string) bool {
	t.Helper()

	ok, err := c.dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(hash)).
		Exist(ctx)
	require.NoError(t, err)

	return ok
}

// narInfoExists reports whether a narinfo row with the given hash is present.
func narInfoExists(ctx context.Context, t *testing.T, c *Cache, hash string) bool {
	t.Helper()

	ok, err := c.dbClient.Ent().NarInfo.Query().
		Where(entnarinfo.HashEQ(hash)).
		Exist(ctx)
	require.NoError(t, err)

	return ok
}

// seedSharedNar creates one nar_file linked by the given narinfo hashes via the
// NarInfoNarFile M:N join, modelling the n:1 relationship where several narinfos
// point at one NAR. When withBytes is true the NAR bytes are also written to the
// store (so deletion of the bytes can be asserted); when false the nar_file is a
// backing-less record (so the missing-NAR read branch fires).
func seedSharedNar(
	ctx context.Context,
	t *testing.T,
	c *Cache,
	narHash string,
	withBytes bool,
	narInfoHashes ...string,
) nar.URL {
	t.Helper()

	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeXz.String()).
		SetQuery("").
		SetFileSize(16).
		SetTotalChunks(0).
		Save(ctx)
	require.NoError(t, err)

	narURL := nar.URL{Hash: narHash, Compression: nar.CompressionTypeXz, Query: url.Values{}}

	if withBytes {
		_, err = c.narStore.PutNar(ctx, narURL, strings.NewReader("dummy-nar-bytes!"), -1)
		require.NoError(t, err)
	}

	for _, nih := range narInfoHashes {
		ni, err := c.dbClient.Ent().NarInfo.Create().
			SetHash(nih).
			SetURL("nar/" + narHash + ".nar.xz").
			Save(ctx)
		require.NoError(t, err)

		_, err = c.dbClient.Ent().NarInfoNarFile.Create().
			SetNarinfoID(ni.ID).
			SetNarFileID(nf.ID).
			Save(ctx)
		require.NoError(t, err)
	}

	return narURL
}

// TestGetNarInfoFromStore_MissingNarDoesNotPurge verifies the store-sourced read
// path is non-destructive: a narinfo whose backing NAR is missing reports the
// cache-miss sentinel without deleting the narinfo or nar_file records. The
// substituter path then self-heals via upstream; deleting here would race a
// concurrent `nix copy` across replicas sharing one DB.
func TestGetNarInfoFromStore_MissingNarDoesNotPurge(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	ctx := newContext()

	// Seed the narinfo into the narinfo store (no NAR bytes) so getNarInfoFromStore
	// finds and parses it, then detects the missing backing NAR.
	ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)
	require.NoError(t, c.narInfoStore.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, ni))

	_, err = c.getNarInfoFromStore(ctx, testdata.Nar1.NarInfoHash)
	require.ErrorIs(t, err, ErrNarInfoPurged,
		"a missing-NAR narinfo on the store path must resolve to the cache-miss sentinel")

	require.True(t, c.narInfoStore.HasNarInfo(ctx, testdata.Nar1.NarInfoHash),
		"store-path read must NOT purge the narinfo from the store")
}

// TestGetNarInfoFromDatabase_NonUploadOnly_PreservesNarFileRecord covers the spec
// scenario "a concurrent substituter read does not invalidate an in-flight
// upload's reference" at the invariant level: a substituter read that fires the
// missing-NAR branch must leave BOTH the narinfo and the (possibly shared)
// nar_file records intact, so a concurrent reference-verification of the same hash
// still observes it present. Were either record deleted (the old behavior, made
// globally visible by the shared production database), the in-flight upload would
// abort with "the reference does not exist".
func TestGetNarInfoFromDatabase_NonUploadOnly_PreservesNarFileRecord(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	ctx := newContext()

	hashA := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	// Backing-less nar_file shared via the join, modelling a reference whose bytes
	// are momentarily absent on the NFS backend.
	seedSharedNar(ctx, t, c, narHash, false, hashA)

	_, err := c.getNarInfoFromDatabase(ctx, hashA)
	require.ErrorIs(t, err, ErrNarInfoPurged,
		"a missing-NAR substituter read must report the cache-miss sentinel")

	assert.True(t, narInfoExists(ctx, t, c, hashA),
		"substituter read must NOT delete the narinfo row (would invalidate the reference)")
	assert.True(t, narFileExists(ctx, t, c, narHash),
		"substituter read must NOT delete the nar_file row (would invalidate the reference)")
}

// TestPurgeNarInfo_SharedNarFileSurvives is the refcount-safety guard the n:1
// relationship demands: purging narinfo A must NOT delete a nar_file (or its NAR
// bytes) still linked by narinfo B.
func TestPurgeNarInfo_SharedNarFileSurvives(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	ctx := newContext()

	hashA := testhelper.MustRandBase32NarHash()
	hashB := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	narURL := seedSharedNar(ctx, t, c, narHash, true, hashA, hashB)

	require.NoError(t, c.purgeNarInfo(ctx, hashA, &narURL))

	assert.False(t, narInfoExists(ctx, t, c, hashA), "purged narinfo A must be deleted")
	assert.True(t, narInfoExists(ctx, t, c, hashB), "sibling narinfo B must survive")
	assert.True(t, narFileExists(ctx, t, c, narHash),
		"the shared nar_file must survive because B still links it")

	present, err := c.narStore.StatNar(ctx, narURL)
	require.NoError(t, err)
	assert.True(t, present, "the shared NAR bytes must NOT be deleted while B links the nar_file")
}

// TestPurgeNarInfo_OrphanedNarFileDeleted verifies the other side of the refcount
// rule: when the purged narinfo is the sole linker, the now-orphaned nar_file and
// its bytes are reclaimed.
func TestPurgeNarInfo_OrphanedNarFileDeleted(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	ctx := newContext()

	hashC := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	narURL := seedSharedNar(ctx, t, c, narHash, true, hashC)

	require.NoError(t, c.purgeNarInfo(ctx, hashC, &narURL))

	assert.False(t, narInfoExists(ctx, t, c, hashC), "purged narinfo C must be deleted")
	assert.False(t, narFileExists(ctx, t, c, narHash),
		"the now-orphaned nar_file (zero links) must be deleted")

	present, err := c.narStore.StatNar(ctx, narURL)
	require.NoError(t, err)
	assert.False(t, present, "the orphaned NAR bytes must be reclaimed")
}
