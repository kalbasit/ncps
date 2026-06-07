package ncps_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	entnarfile "github.com/kalbasit/ncps/ent/narfile"
	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"
	chunkstore "github.com/kalbasit/ncps/pkg/storage/chunk"

	"github.com/kalbasit/ncps/ent"
	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/testdata"
)

// setupResidueChunkedNar wires a CDC-enabled SQLite cache with a single chunked
// nar_file (stored under Compression:none, as production chunked NARs are) linked
// to Nar1's narinfo. The narinfo carries Nar1's NarHash and its non-none URL
// (nar/<H>.nar.xz), i.e. the recoverable-but-inconsistent baseline that each test
// then mutates. Returns the app, db client, storage dir, db URL, and the chunked
// nar_file's hash.
func setupResidueChunkedNar(
	ctx context.Context, t *testing.T,
) (*cli.Command, *database.Client, string, string, string) {
	t.Helper()

	dbClient, _, dir, dbURL, cleanup := setupFsckSQLite(t)
	t.Cleanup(cleanup)

	configureFsckCDCInDatabase(ctx, t, dbClient)

	cs, err := chunkstore.NewLocalStore(filepath.Join(dir, "store"))
	require.NoError(t, err)

	setupFsckCDCNarFile(ctx, t, dbClient, cs,
		testdata.Nar1.NarInfoHash, testdata.Nar1.NarInfoText,
		testdata.Nar1.NarHash, nar.CompressionTypeNone, 2)

	app, err := ncps.New()
	require.NoError(t, err)

	return app, dbClient, dir, dbURL, testdata.Nar1.NarHash
}

func runResidueFsckRepair(ctx context.Context, t *testing.T, app *cli.Command, dbURL, dir string) {
	t.Helper()

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "fsck",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--repair",
	}))
}

func makeUnDeChunkable(ctx context.Context, t *testing.T, dbClient *database.Client) {
	t.Helper()

	// Removing the only narinfo NarHash leaves nothing to content-verify against, so
	// the de-chunk resolver classifies the chunked NAR as un-de-chunkable residue.
	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		ClearNarHash().
		Save(ctx)
	require.NoError(t, err)
}

func countChunkedNarFiles(ctx context.Context, t *testing.T, dbClient *database.Client) int {
	t.Helper()

	var n int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM nar_files WHERE total_chunks > 0").Scan(&n))

	return n
}

// 3.1 Recoverable inconsistent chunked NAR is normalized, not purged.
func TestFsckChunkedResidue_RecoverableNormalizedNotPurged(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	app, dbClient, dir, dbURL, hash := setupResidueChunkedNar(ctx, t)

	// Baseline narinfo has a valid NarHash but a non-none URL (nar/<H>.nar.xz).
	runResidueFsckRepair(ctx, t, app, dbURL, dir)

	ni, err := dbClient.Ent().NarInfo.Query().Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, ni.URL)
	assert.Equal(t, "nar/"+hash+".nar", *ni.URL, "recoverable narinfo URL must be normalized to Compression:none")
	require.NotNil(t, ni.Compression)
	assert.Equal(t, nar.CompressionTypeNone.String(), *ni.Compression)

	nf, err := getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err, "nar_file must remain (not purged)")
	assert.Positive(t, nf.TotalChunks, "nar_file must remain chunked")
}

// TestFsckChunkedResidue_E2ELifecycle is the CDC-lifecycle end-to-end test: it
// exercises the fsck reclaimer alongside the drain using a real CDC-chunked NAR
// (real chunks, verifiable NarHash), then walks the full residue lifecycle —
// legitimately-chunked NAR left untouched, residue flagged-not-purged on first
// detection, reclaimed only after the grace window on a later run, and the drain
// finding a clean, consistent state afterwards.
//
//nolint:tparallel // sequential lifecycle stages share one DB; subtests must not interleave
func TestFsckChunkedResidue_E2ELifecycle(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	// Real CDC chunking via the CLI path, with a NarHash that matches the chunk
	// content — a legitimately chunked, recoverable NAR.
	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	fsckRepair := func() {
		require.NoError(t, app.Run(ctx, []string{
			"ncps", "fsck", "--cache-database-url", dbURL, "--cache-storage-local", dir, "--repair",
		}))
	}

	chunkedNarFile := func() *ent.NarFile {
		nf, err := dbClient.Ent().NarFile.Query().Where(entnarfile.TotalChunksGT(0)).Only(ctx)
		require.NoError(t, err)

		return nf
	}

	// setupChunkedNar uses random NAR bytes, so align the narinfo nar_size with the
	// real chunked file_size — otherwise the generic CDC size-mismatch check would
	// delete the row before the residue tiers run.
	nf0 := chunkedNarFile()
	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetNarSize(int64(nf0.FileSize)). //nolint:gosec // file_size is a non-negative byte count
		Save(ctx)
	require.NoError(t, err)

	// Stage 1: a legitimately chunked NAR (resolvable, consistent) is left untouched —
	// fsck must never harm a healthy chunked NAR during active CDC.
	fsckRepair()

	nf := chunkedNarFile()
	require.Positive(t, nf.TotalChunks, "legitimately chunked NAR must remain chunked")
	require.Nil(t, nf.DechunkResidueFlaggedAt, "a healthy chunked NAR must not be flagged")

	// Stage 2: it becomes residue (no narinfo NarHash to verify against). First fsck
	// run flags it but must not purge.
	makeUnDeChunkable(ctx, t, dbClient)
	fsckRepair()

	nf = chunkedNarFile()
	require.Positive(t, nf.TotalChunks, "residue must not be purged on first detection")
	require.NotNil(t, nf.DechunkResidueFlaggedAt, "residue must be flagged on first detection")

	// Stage 3: a day passes (flag ages past the grace window). The next fsck run
	// reclaims it.
	_, err = dbClient.Ent().NarFile.UpdateOneID(nf.ID).
		SetDechunkResidueFlaggedAt(time.Now().Add(-48 * time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	fsckRepair()
	assert.Zero(t, countChunkedNarFiles(ctx, t, dbClient), "aged residue must be reclaimed on a later run")

	// Stage 4: the operator drain now finds a clean, consistent state (nothing to
	// migrate) and exits successfully — de-chunk + fsck together converge.
	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar", "--cache-database-url", dbURL, "--cache-storage-local", dir,
	}), "drain must succeed against the clean post-fsck state")
}

// 3.2 First detection flags but does not purge.
func TestFsckChunkedResidue_FirstDetectionFlagsNotPurges(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	app, dbClient, dir, dbURL, hash := setupResidueChunkedNar(ctx, t)

	makeUnDeChunkable(ctx, t, dbClient)

	runResidueFsckRepair(ctx, t, app, dbURL, dir)

	nf, err := getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err, "first detection must not purge")
	assert.Positive(t, nf.TotalChunks, "first detection must not purge the chunked nar_file")
	require.NotNil(t, nf.DechunkResidueFlaggedAt, "first detection must set the residue flag")
}

// 3.3 Aged + still-un-de-chunkable row is purged on a later run.
func TestFsckChunkedResidue_AgedUnDeChunkablePurged(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	app, dbClient, dir, dbURL, hash := setupResidueChunkedNar(ctx, t)

	makeUnDeChunkable(ctx, t, dbClient)

	nf, err := getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err)

	// Simulate a prior fsck run that flagged it more than the grace window ago.
	_, err = dbClient.Ent().NarFile.UpdateOneID(nf.ID).
		SetDechunkResidueFlaggedAt(time.Now().Add(-48 * time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	runResidueFsckRepair(ctx, t, app, dbURL, dir)

	_, err = getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.Error(t, err, "aged un-de-chunkable nar_file must be purged")
	assert.Zero(t, countChunkedNarFiles(ctx, t, dbClient), "no chunked nar_files should remain after reclamation")
}

// 3.4 A row that became recoverable is unflagged, never purged.
func TestFsckChunkedResidue_BecameRecoverableUnflagged(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	app, dbClient, dir, dbURL, hash := setupResidueChunkedNar(ctx, t)

	// The narinfo still carries a valid NarHash (recoverable), but the row was flagged
	// on a previous run while it was transiently un-de-chunkable.
	nf, err := getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err)

	_, err = dbClient.Ent().NarFile.UpdateOneID(nf.ID).
		SetDechunkResidueFlaggedAt(time.Now().Add(-48 * time.Hour)).
		Save(ctx)
	require.NoError(t, err)

	runResidueFsckRepair(ctx, t, app, dbURL, dir)

	nf, err = getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err, "recoverable row must not be purged")
	assert.Positive(t, nf.TotalChunks, "recoverable row must remain chunked")
	assert.Nil(t, nf.DechunkResidueFlaggedAt, "became-recoverable row must be unflagged")
}

// 3.5 A row with a recent chunking_started_at is not flagged/purged.
func TestFsckChunkedResidue_InFlightChunkingUntouched(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	app, dbClient, dir, dbURL, hash := setupResidueChunkedNar(ctx, t)

	makeUnDeChunkable(ctx, t, dbClient)

	nf, err := getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err)

	// Aged flag (would otherwise reclaim) BUT chunking_started_at is recent: the NAR is
	// actively being written, so fsck must leave it completely untouched.
	flaggedAt := time.Now().Add(-48 * time.Hour)
	_, err = dbClient.Ent().NarFile.UpdateOneID(nf.ID).
		SetDechunkResidueFlaggedAt(flaggedAt).
		SetChunkingStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	runResidueFsckRepair(ctx, t, app, dbURL, dir)

	nf, err = getNarFile(ctx, dbClient, hash, nar.CompressionTypeNone.String(), "")
	require.NoError(t, err, "in-flight chunking row must not be purged")
	assert.Positive(t, nf.TotalChunks, "in-flight chunking row must remain chunked")
	require.NotNil(t, nf.DechunkResidueFlaggedAt, "in-flight chunking row's flag must be left untouched")
	assert.WithinDuration(t, flaggedAt, *nf.DechunkResidueFlaggedAt, time.Second,
		"in-flight chunking row's flag must be unchanged")
}
