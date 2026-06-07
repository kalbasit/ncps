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

// dropLastChunkLink deletes one nar_file_chunks junction row for narFileID (the
// highest chunk_index), simulating the production
// nar_file_chunks.chunk_id -> chunks(id) ON DELETE CASCADE loss that leaves a
// completed NAR with total_chunks > remaining links.
func dropLastChunkLink(ctx context.Context, t *testing.T, dbClient *database.Client, narFileID int) {
	t.Helper()

	links, err := dbClient.Ent().NarFileChunk.Query().
		Where(entnarfilechunk.NarFileIDEQ(narFileID)).
		Order(entnarfilechunk.ByChunkIndex()).
		All(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, links)

	_, err = dbClient.Ent().NarFileChunk.Delete().
		Where(entnarfilechunk.IDEQ(links[len(links)-1].ID)).
		Exec(ctx)
	require.NoError(t, err)
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

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false))

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

// TestMigrateChunksToNar_NormalizesNarinfoURLToNoneOnDeChunk covers class (B): when
// a NAR is de-chunked to none/whole, its narinfo URL must be normalized to the
// Compression:none form. Otherwise a narinfo advertising .nar.xz would 404 once the
// NAR is no longer chunked (serve-time normalization only fires while chunks exist).
func TestMigrateChunksToNar_NormalizesNarinfoURLToNoneOnDeChunk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// The narinfo advertises a different-compression URL with a stale file_hash.
	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetURL("nar/" + noneURL.Hash + ".nar.xz").
		SetCompression(nar.CompressionTypeXz.String()).
		SetFileHash("sha256:staleXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX").
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false))

	row, err := dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).Only(ctx)
	require.NoError(t, err)

	require.NotNil(t, row.URL)
	assert.Equal(t, "nar/"+noneURL.Hash+".nar", *row.URL,
		"de-chunk must normalize the narinfo URL to the Compression:none form")
	require.NotNil(t, row.Compression)
	assert.Equal(t, nar.CompressionTypeNone.String(), *row.Compression,
		"de-chunk must set the narinfo compression to none")
	assert.Nil(t, row.FileHash, "a Compression:none narinfo must have a null file_hash")
}

// TestMigrateChunksToNar_DeChunksWhenNarinfoURLHasDifferentCompression covers the
// self-completing-drain class (A): a chunked NAR whose only NarHash-bearing narinfo
// advertises a DIFFERENT-compression URL (nar/<hash>.nar.xz) than the bare none URL,
// with no join link. The verify NarHash must be resolved by NAR hash from that
// narinfo (the hash is the uncompressed content hash, identical across compressions),
// so the NAR is reconstructed, content-verified, and de-chunked instead of skipped.
func TestMigrateChunksToNar_DeChunksWhenNarinfoURLHasDifferentCompression(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, content := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// The only NarHash-bearing narinfo advertises .nar.xz (not the bare none URL),
	// and there is no join link — exactly the stranded production class.
	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetURL("nar/" + noneURL.Hash + ".nar.xz").
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Delete().Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false),
		"a chunked NAR whose only NarHash-bearing narinfo is at a different-compression URL must still de-chunk")

	assert.True(t, c.HasNarInStore(ctx, noneURL),
		"the whole NAR must be present after de-chunking the different-compression-URL NAR")

	_, _, rc, err := c.GetNar(ctx, noneURL)
	require.NoError(t, err)

	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, content, string(data), "reconstruction must be content-verified and correct")
}

// TestMigrateChunksToNar_DeChunksUnlinkedNarViaURLFallback is Fix A for the
// drain-mode stuck NARs: a completed chunked NAR whose narinfo_nar_files join link
// was never created (a known race between the narinfo-write link creation and the
// async CDC chunking that finalizes the nar_file) must still be de-chunked. The
// verify NarHash is resolved via the narinfo's Compression:none URL instead of the
// missing link, so the NAR is drained rather than skipped forever (which would keep
// the cache stuck in drain mode).
func TestMigrateChunksToNar_DeChunksUnlinkedNarViaURLFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, content := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// Sever the join link — model the production race that left the chunked
	// nar_file unlinked from its (still-present, NarHash-bearing) narinfo.
	_, err := dbClient.Ent().NarInfoNarFile.Delete().Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false),
		"an unlinked chunked NAR must still de-chunk via the url-based NarHash fallback")

	assert.True(t, c.HasNarInStore(ctx, noneURL),
		"the whole NAR must be present in the store after de-chunking the unlinked NAR")

	_, _, rc, err := c.GetNar(ctx, noneURL)
	require.NoError(t, err)

	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, content, string(data), "reconstruction of the unlinked NAR must be correct")
}

// TestMigrateChunksToNar_DeChunksUnlinkedPrefixedNarinfoURL covers
// fix-dechunk-unlinked-narinfo-url-match: an unlinked narinfo whose URL is the
// nix-serve-style prefixed form (nar/<narinfoHash>-<H>.nar.xz) must be matched
// hash-aware. The old raw URLHasPrefix("nar/"+H+".") fallback missed it, so the
// verify NarHash could not be resolved (NAR purged / drain stuck) and the narinfo
// URL was left unnormalized (→ later 404). The hash-aware match normalizes the
// candidate URL's embedded hash and recognizes it as referencing the NAR.
func TestMigrateChunksToNar_DeChunksUnlinkedPrefixedNarinfoURL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, content := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// The only NarHash-bearing narinfo advertises a nix-serve-style PREFIXED URL
	// (narinfoHash prepended to the nar hash), and there is no join link — the
	// stranded class the raw prefix match misses.
	prefixedURL := "nar/" + testdata.Nar1.NarInfoHash + "-" + noneURL.Hash + ".nar.xz"

	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetURL(prefixedURL).
		SetCompression(nar.CompressionTypeXz.String()).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Delete().Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false),
		"an unlinked prefixed-URL narinfo must be matched hash-aware and the NAR de-chunked")

	assert.True(t, c.HasNarInStore(ctx, noneURL),
		"the whole NAR must be present after de-chunking via the hash-aware match")

	row, err := dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, row.URL)
	assert.Equal(t, "nar/"+noneURL.Hash+".nar", *row.URL,
		"the prefixed-URL narinfo must be normalized to the Compression:none URL on de-chunk")
	require.NotNil(t, row.Compression)
	assert.Equal(t, nar.CompressionTypeNone.String(), *row.Compression)

	_, _, rc, err := c.GetNar(ctx, noneURL)
	require.NoError(t, err)

	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, content, string(data), "reconstruction of the unlinked prefixed-URL NAR must be correct")
}

// TestNormalizeChunkedNarInfoURL_MatchesUnlinkedPrefixedURL covers the
// NormalizeChunkedNarInfoURL site of fix-dechunk-unlinked-narinfo-url-match: the
// fsck residue repair must normalize an unlinked narinfo with a nix-serve-style
// prefixed URL via the hash-aware match (leaving the NAR chunked), where the old
// raw URLHasPrefix fallback left it stranded.
func TestNormalizeChunkedNarInfoURL_MatchesUnlinkedPrefixedURL(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	prefixedURL := "nar/" + testdata.Nar1.NarInfoHash + "-" + noneURL.Hash + ".nar.xz"

	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetURL(prefixedURL).
		SetCompression(nar.CompressionTypeXz.String()).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Delete().Exec(ctx)
	require.NoError(t, err)

	require.NoError(t, c.NormalizeChunkedNarInfoURL(ctx, &noneURL),
		"an unlinked prefixed-URL narinfo must be matched hash-aware and normalized")

	row, err := dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, row.URL)
	assert.Equal(t, "nar/"+noneURL.Hash+".nar", *row.URL,
		"NormalizeChunkedNarInfoURL must normalize an unlinked prefixed-URL narinfo to none")
	require.NotNil(t, row.Compression)
	assert.Equal(t, nar.CompressionTypeNone.String(), *row.Compression)
}

// TestMigrateChunksToNar_ResumesWhenWholeFileAlreadyPresent: an interrupted
// prior run may have written the (verified) whole file but crashed before the
// record flip. Re-running must treat the already-present object as resumable —
// PutNar's ErrAlreadyExists is not fatal — and still flip the record.
func TestMigrateChunksToNar_ResumesWhenWholeFileAlreadyPresent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, localStore, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, content := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// Simulate the interrupted state: whole file already in the store, record still chunked.
	_, err := localStore.PutNar(ctx, noneURL, strings.NewReader(content), int64(len(content)))
	require.NoError(t, err)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false),
		"an already-present whole file must be treated as resumable, not fatal")

	nf, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash), entnarfile.CompressionEQ(nar.CompressionTypeNone.String())).
		Only(ctx)
	require.NoError(t, err)
	assert.Zero(t, nf.TotalChunks, "the record must still be flipped to whole-file on resume")
}

// TestMigrateChunksToNar_MissingLinkIsReportedAsMissingChunk verifies the drain
// hardening: a completed chunked NAR that lost a junction link (the production
// chunks(id) ON DELETE CASCADE corruption — total_chunks unchanged, links < N)
// is reported by MigrateChunksToNar as cache.ErrMissingChunk. That is the signal
// the migrate driver loop uses to purge the NAR and continue (so the hash is
// re-fetched from upstream), rather than reconstructing a truncated NAR or
// aborting the run. This exercises the serving-integrity guard end-to-end through
// the migration path.
func TestMigrateChunksToNar_MissingLinkIsReportedAsMissingChunk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	nf, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash), entnarfile.CompressionEQ(nar.CompressionTypeNone.String())).
		Only(ctx)
	require.NoError(t, err)
	require.Positive(t, nf.TotalChunks, "fixture must produce a chunked NAR")

	// Simulate the cascade loss: drop one junction link, leaving total_chunks intact.
	dropLastChunkLink(ctx, t, dbClient, nf.ID)

	err = c.MigrateChunksToNar(ctx, &noneURL, false)
	require.ErrorIs(t, err, cache.ErrMissingChunk,
		"a completed chunked NAR missing a junction link must be reported as ErrMissingChunk so the driver purges it")
}

// TestPurgeChunkedNar_LeavesCleanCacheMissForRefetch verifies the self-heal
// contract: purging an un-reassemblable chunked NAR removes its nar_file record
// (and chunk links) so HasNarInChunks/HasNarInStore both report absent — a clean
// cache miss the next GetNar resolves by refetching from upstream — while leaving
// the linked narinfo intact so that refetch can proceed.
func TestPurgeChunkedNar_LeavesCleanCacheMissForRefetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	// Drop a junction link so the NAR is permanently un-reassemblable.
	nf, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash), entnarfile.CompressionEQ(nar.CompressionTypeNone.String())).
		Only(ctx)
	require.NoError(t, err)

	dropLastChunkLink(ctx, t, dbClient, nf.ID)

	require.NoError(t, c.PurgeChunkedNar(ctx, &noneURL))

	// After purge: not servable from chunks, not in the whole-file store → a clean
	// cache miss. The linked narinfo remains so a subsequent request refetches.
	hasChunks, err := c.HasNarInChunks(ctx, noneURL)
	require.NoError(t, err)
	assert.False(t, hasChunks, "purged NAR must no longer be servable from chunks")
	assert.False(t, c.HasNarInStore(ctx, noneURL), "purged NAR must not be in the whole-file store")

	count, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, count, "purged NAR's nar_file record must be deleted so the next GetNar is a cache miss")
}

// TestMigrateChunksToNar_FlipsRecordToWholeFile (slice 2): the nar_file is
// flipped to the whole-file representation (total_chunks=0, no chunk links).
func TestMigrateChunksToNar_FlipsRecordToWholeFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false))

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

// TestMigrateChunksToNar_DefaultLeavesChunksForGC (slice 3): by default the
// migration flips the record but does NOT delete the now-orphaned chunks — they
// are left for the GC so an in-flight chunk-serve is never truncated.
func TestMigrateChunksToNar_DefaultLeavesChunksForGC(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	before, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	require.Positive(t, before, "fixture should have created chunks")

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, false))

	after, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after, "default migration must NOT delete chunks (left for GC)")
}

// TestMigrateChunksToNar_ForceReclaimDeletesOrphans (slice 3): with
// forceReclaim, chunks referenced only by the migrated NAR are deleted.
func TestMigrateChunksToNar_ForceReclaimDeletesOrphans(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	before, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	require.Positive(t, before, "fixture should have created chunks")

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, true))

	after, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, after, "force-reclaim must delete chunks orphaned by de-chunking the only referencing NAR")
}

// TestPurgeChunkedNar_DeletesLinksChunksAndRecord: PurgeChunkedNar removes all
// nar_file_chunks links, deletes now-orphaned chunk objects from the chunk store,
// and deletes the nar_file record so the hash can be re-fetched from upstream.
func TestPurgeChunkedNar_DeletesLinksChunksAndRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	chunksBefore, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	require.Positive(t, chunksBefore, "precondition: fixture must have created chunks")

	require.NoError(t, c.PurgeChunkedNar(ctx, &noneURL))

	// nar_file record must be gone
	narFileCount, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, narFileCount, "PurgeChunkedNar must delete the nar_file record")

	// all chunk links must be gone
	linkCount, err := dbClient.Ent().NarFileChunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, linkCount, "PurgeChunkedNar must delete all nar_file_chunks links")

	// orphaned chunk objects must be gone
	chunksAfter, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, chunksAfter, "PurgeChunkedNar must delete orphaned chunk objects")
}

// TestPurgeChunkedNar_RetainsSharedChunk: a chunk still referenced by a second
// nar_file must not be deleted (dedup-safe reclamation).
func TestPurgeChunkedNar_RetainsSharedChunk(t *testing.T) {
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

	// A second nar_file that shares the same chunks (simulates cross-NAR dedup).
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

	chunksBefore, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)

	require.NoError(t, c.PurgeChunkedNar(ctx, &noneURL))

	// nf1 nar_file must be gone, nf2 must remain
	narFileCount, err := dbClient.Ent().NarFile.Query().
		Where(entnarfile.HashEQ(noneURL.Hash)).
		Count(ctx)
	require.NoError(t, err)
	assert.Zero(t, narFileCount, "nar_file for the purged hash must be deleted")

	// shared chunk objects must be retained
	chunksAfter, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, chunksBefore, chunksAfter,
		"chunks still referenced by another nar_file must not be deleted")
}

// TestPurgeChunkedNar_RetainsNarInfoRecord: purging a NAR must leave the linked
// narinfo record intact so the next GetNarInfo can succeed and trigger a re-fetch.
func TestPurgeChunkedNar_RetainsNarInfoRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, dbClient, _, dir, _, cleanup := setupSQLiteFactory(t)
	t.Cleanup(cleanup)

	noneURL, _ := chunkedNarFixture(ctx, t, c, dbClient, dir)

	narInfoBefore, err := dbClient.Ent().NarInfo.Query().Count(ctx)
	require.NoError(t, err)
	require.Positive(t, narInfoBefore, "precondition: narinfo must exist")

	require.NoError(t, c.PurgeChunkedNar(ctx, &noneURL))

	narInfoAfter, err := dbClient.Ent().NarInfo.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, narInfoBefore, narInfoAfter,
		"PurgeChunkedNar must not delete narinfo records")
}

// TestMigrateChunksToNar_ForceReclaimRetainsSharedChunks (slice 3): even with
// forceReclaim, chunks still referenced by another nar_file are NOT deleted
// (dedup-safe reclamation).
func TestMigrateChunksToNar_ForceReclaimRetainsSharedChunks(t *testing.T) {
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

	require.NoError(t, c.MigrateChunksToNar(ctx, &noneURL, true))

	after, err := dbClient.Ent().Chunk.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, before, after, "chunks shared with another nar_file must be retained even with force-reclaim")
}
