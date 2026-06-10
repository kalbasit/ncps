package ncps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
)

// TestRepairNarInfoCompressionDesync verifies the bulk data-repair for the still
// un-servable inverse of the compression desync (issue #1392): a narinfo
// advertising a non-producible compression (xz) whose backing NAR is stored
// otherwise. It MUST be rewritten to the servable none form, healthy narinfos
// MUST be left alone, and the repair MUST be idempotent.
func TestRepairNarInfoCompressionDesync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dbClient := newCDCModeTestDB(t)

	// Desynced: narinfo advertises xz, but the only backing nar_file is none.
	driftedHash := testdata.Nar1.NarHash
	driftedNF, err := dbClient.Ent().NarFile.Create().
		SetHash(driftedHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(100).
		Save(ctx)
	require.NoError(t, err)

	driftedNI, err := dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar1.NarInfoHash).
		SetStorePath("/nix/store/" + testdata.Nar1.NarInfoHash + "-pkg").
		SetURL("nar/" + driftedHash + ".nar.xz").
		SetCompression(nar.CompressionTypeXz.String()).
		SetFileHash("sha256:" + driftedHash).
		SetFileSize(50).
		SetNarSize(100).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(driftedNI.ID).
		SetNarFileID(driftedNF.ID).
		Save(ctx)
	require.NoError(t, err)

	// Healthy: narinfo advertises xz and an xz nar_file actually exists.
	healthyHash := testdata.Nar2.NarHash
	healthyNF, err := dbClient.Ent().NarFile.Create().
		SetHash(healthyHash).
		SetCompression(nar.CompressionTypeXz.String()).
		SetQuery("").
		SetFileSize(80).
		Save(ctx)
	require.NoError(t, err)

	healthyNI, err := dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar2.NarInfoHash).
		SetStorePath("/nix/store/" + testdata.Nar2.NarInfoHash + "-pkg").
		SetURL("nar/" + healthyHash + ".nar.xz").
		SetCompression(nar.CompressionTypeXz.String()).
		SetFileHash("sha256:" + healthyHash).
		SetFileSize(80).
		SetNarSize(160).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(healthyNI.ID).
		SetNarFileID(healthyNF.ID).
		Save(ctx)
	require.NoError(t, err)

	// Repair.
	repaired, err := repairNarInfoCompressionDesync(ctx, dbClient)
	require.NoError(t, err)
	assert.Equal(t, 1, repaired, "exactly the one desynced narinfo must be repaired")

	// The desynced narinfo is rewritten to the servable none form.
	got, err := dbClient.Ent().NarInfo.Get(ctx, driftedNI.ID)
	require.NoError(t, err)
	require.NotNil(t, got.URL)
	assert.Equal(t, "nar/"+driftedHash+".nar", *got.URL, "URL must drop the .xz extension")
	require.NotNil(t, got.Compression)
	assert.Equal(t, nar.CompressionTypeNone.String(), *got.Compression, "compression must become none")
	assert.Nil(t, got.FileHash, "FileHash must be cleared for a none narinfo")
	assert.Nil(t, got.FileSize, "FileSize must be cleared for a none narinfo")

	// The healthy narinfo is untouched.
	stillHealthy, err := dbClient.Ent().NarInfo.Get(ctx, healthyNI.ID)
	require.NoError(t, err)
	require.NotNil(t, stillHealthy.Compression)
	assert.Equal(t, nar.CompressionTypeXz.String(), *stillHealthy.Compression, "healthy xz narinfo must be untouched")

	// Idempotent: a second run repairs nothing.
	again, err := repairNarInfoCompressionDesync(ctx, dbClient)
	require.NoError(t, err)
	assert.Equal(t, 0, again, "a second repair run must be a no-op")
}
