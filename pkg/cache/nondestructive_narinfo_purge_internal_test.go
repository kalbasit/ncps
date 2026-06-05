package cache

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/storage/chunk"
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

// TestGetNarInfoFromDatabase_TrustsBytesStoredMarker covers the multi-replica
// cross-consistency fix: a NAR whose bytes were durably stored by a peer replica
// is recorded in the shared database (nar_file.bytes_stored_at set by PutNar) before
// this replica's local filesystem stat observes the write. The /upload narinfo
// read MUST treat such a row as present — trusting the shared-DB marker over the
// local stat — so a concurrent `nix copy` reference check does not 404 a NAR
// another replica just uploaded and abort.
func TestGetNarInfoFromDatabase_TrustsBytesStoredMarker(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	// The marker is trusted only on the upload (/upload) path — the substituter path
	// must keep self-healing a genuinely missing NAR via upstream.
	ctx := WithUploadOnly(newContext())

	hashA := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	// nar_file marked bytes-stored (as PutNar does after writing bytes) but with NO
	// bytes in THIS replica's store — models a peer's upload not yet locally visible.
	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeXz.String()).
		SetQuery("").
		SetFileSize(16).
		SetTotalChunks(0).
		SetBytesStoredAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	niRow, err := c.dbClient.Ent().NarInfo.Create().
		SetHash(hashA).
		SetURL("nar/" + narHash + ".nar.xz").
		Save(ctx)
	require.NoError(t, err)

	_, err = c.dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(niRow.ID).
		SetNarFileID(nf.ID).
		Save(ctx)
	require.NoError(t, err)

	got, err := c.getNarInfoFromDatabase(ctx, hashA)
	require.NoError(t, err,
		"a bytes-stored nar_file must be treated as present even without local bytes")
	require.NotNil(t, got)
	require.NotErrorIs(t, err, ErrNarInfoPurged)
}

// TestGetNarInfoFromDatabase_BytesStoredMarkerIsCompressionAgnostic covers the prod
// CDC-residue case: a narinfo advertises url=nar/<hash>.nar (Compression:none) while
// its backing NAR is durably stored only under a DIFFERENT compression (xz) — a
// nar_file row keyed by xz with bytes_stored_at set, no compression=none row at all.
// The /upload presence check must match the nar_file by hash (+query) regardless of
// compression, so the reference reports present. A compression-keyed lookup misses
// the xz row and wrongly 404s the NAR, aborting a concurrent `nix copy`.
func TestGetNarInfoFromDatabase_BytesStoredMarkerIsCompressionAgnostic(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	// The marker is trusted only on the upload (/upload) path.
	ctx := WithUploadOnly(newContext())

	hashA := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	// NAR durably stored under XZ (bytes_stored_at set), with NO compression=none row
	// and no local bytes — the narinfo URL nevertheless advertises compression=none.
	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeXz.String()).
		SetQuery("").
		SetFileSize(16).
		SetTotalChunks(0).
		SetBytesStoredAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	niRow, err := c.dbClient.Ent().NarInfo.Create().
		SetHash(hashA).
		SetURL("nar/" + narHash + ".nar"). // Compression:none URL (CDC residue).
		Save(ctx)
	require.NoError(t, err)

	_, err = c.dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(niRow.ID).
		SetNarFileID(nf.ID).
		Save(ctx)
	require.NoError(t, err)

	got, err := c.getNarInfoFromDatabase(ctx, hashA)
	require.NoError(t, err,
		"a bytes-stored nar_file must be treated as present even when the narinfo URL "+
			"advertises a different compression than the stored NAR")
	require.NotNil(t, got)
	require.NotErrorIs(t, err, ErrNarInfoPurged)
}

// TestGetNarInfoFromDatabase_UnverifiedMissingNarIsStillAMiss is the phantom-safety
// guard: a nar_file row WITHOUT the bytes_stored_at marker (e.g. the placeholder a
// narinfo PUT creates) and no local bytes MUST still resolve to the missing-NAR
// cache-miss sentinel — trusting the marker must not resurrect phantoms.
func TestGetNarInfoFromDatabase_UnverifiedMissingNarIsStillAMiss(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	// Even on the upload path, an unverified placeholder must not be trusted.
	ctx := WithUploadOnly(newContext())

	hashA := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	// nar_file placeholder: NO verified_at, NO bytes.
	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeXz.String()).
		SetQuery("").
		SetFileSize(16).
		SetTotalChunks(0).
		Save(ctx)
	require.NoError(t, err)

	niRow, err := c.dbClient.Ent().NarInfo.Create().
		SetHash(hashA).
		SetURL("nar/" + narHash + ".nar.xz").
		Save(ctx)
	require.NoError(t, err)

	_, err = c.dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(niRow.ID).
		SetNarFileID(nf.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = c.getNarInfoFromDatabase(ctx, hashA)
	require.ErrorIs(t, err, ErrNarInfoPurged,
		"an unverified, byte-less nar_file must remain a cache miss (no phantom revival)")
}

// TestGetNarInfoFromStore_ChunkBackedNarIsNotAMiss guards the CDC case (PR #1326
// review): the store-sourced read path must check chunk storage, not only
// whole-file storage, before declaring a missing-NAR cache miss. A legacy narinfo
// whose whole-file NAR was replaced by CDC chunks is still locally servable via
// GetNar and must not be turned into a 404/sentinel.
func TestGetNarInfoFromStore_ChunkBackedNarIsNotAMiss(t *testing.T) {
	t.Parallel()

	ctx := newContext()

	c, _, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	chunkStore, err := chunk.NewLocalStore(filepath.Join(dir, "chunks-store"))
	require.NoError(t, err)
	c.SetChunkStore(chunkStore)
	require.NoError(t, c.SetCDCConfiguration(true, 1024, 4096, 8192))

	ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)
	require.NoError(t, c.narInfoStore.PutNarInfo(ctx, testdata.Nar1.NarInfoHash, ni))

	// Chunked backing (CDC stores chunks under the compression=none key), with no
	// whole-file NAR in the store.
	_, err = c.dbClient.Ent().NarFile.Create().
		SetHash(testdata.Nar1.NarHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1024).
		SetTotalChunks(3).
		Save(ctx)
	require.NoError(t, err)

	got, err := c.getNarInfoFromStore(ctx, testdata.Nar1.NarInfoHash)
	require.NotErrorIs(t, err, ErrNarInfoPurged,
		"a chunk-backed (CDC) narinfo is locally servable and must NOT be treated as a cache miss")
	require.NoError(t, err)
	require.NotNil(t, got)
}

// TestPurgeNarInfo_OrphanedNoneNarReclaimsZstdBytes guards PR #1326 review point:
// for compression=none NARs the physical object is stored under the .nar.zst
// variant, so reclaiming an orphaned none nar_file must delete that object, not
// just the logical .nar path.
func TestPurgeNarInfo_OrphanedNoneNarReclaimsZstdBytes(t *testing.T) {
	t.Parallel()

	c, _ := newUploadOnlyPurgeCacheNoSeed(t)

	ctx := newContext()

	hashC := testhelper.MustRandBase32NarHash()
	narHash := testhelper.MustRandBase32NarHash()

	nf, err := c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(16).
		SetTotalChunks(0).
		Save(ctx)
	require.NoError(t, err)

	niRow, err := c.dbClient.Ent().NarInfo.Create().
		SetHash(hashC).
		SetURL("nar/" + narHash + ".nar").
		Save(ctx)
	require.NoError(t, err)

	_, err = c.dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(niRow.ID).
		SetNarFileID(nf.ID).
		Save(ctx)
	require.NoError(t, err)

	noneURL := nar.URL{Hash: narHash, Compression: nar.CompressionTypeNone, Query: url.Values{}}
	zstdURL := nar.URL{Hash: narHash, Compression: nar.CompressionTypeZstd, Query: url.Values{}}

	// none bytes physically live under the .nar.zst variant.
	_, err = c.narStore.PutNar(ctx, zstdURL, strings.NewReader("dummy-zstd-bytes"), -1)
	require.NoError(t, err)

	present, err := c.narStore.StatNar(ctx, zstdURL)
	require.NoError(t, err)
	require.True(t, present, "precondition: the .nar.zst object exists")

	require.NoError(t, c.purgeNarInfo(ctx, hashC, &noneURL))

	present, err = c.narStore.StatNar(ctx, zstdURL)
	require.NoError(t, err)
	assert.False(t, present,
		"reclaiming an orphaned compression=none nar_file must delete its .nar.zst object")
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
