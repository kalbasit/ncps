package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalbasit/ncps/pkg/nar"
	"github.com/kalbasit/ncps/testdata"
)

// TestRecoveryGCKeepsWhenAnyLinkedNarInfoPresent verifies that when several narinfos
// reference the same NAR (same URL, e.g. different store paths sharing a NAR), the
// backing-less placeholder is GC'd only if EVERY linked narinfo is genuinely absent
// upstream. If any one is still present, the row must be kept.
func TestRecoveryGCKeepsWhenAnyLinkedNarInfoPresent(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "5lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	const absentNarInfoHash = "gcmultiabsent00000000000000000aa"

	narURL := nar.URL{Hash: narHash, Compression: nar.CompressionTypeNone}

	nf, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(f.ctx)
	require.NoError(t, err)

	// Insert the ABSENT narinfo first so a single-row First() query would pick it
	// (lowest id) and wrongly conclude the NAR is gone.
	niAbsent, err := f.c.dbClient.Ent().NarInfo.Create().
		SetHash(absentNarInfoHash).
		SetURL(narURL.String()).
		Save(f.ctx)
	require.NoError(t, err)

	// A second narinfo for the same NAR whose narinfo IS still served upstream.
	niPresent, err := f.c.dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar1.NarInfoHash).
		SetURL(narURL.String()).
		Save(f.ctx)
	require.NoError(t, err)

	// Link both narinfos to the nar_file via narinfo_nar_files (the relation the GC
	// resolves through).
	for _, niID := range []int{niAbsent.ID, niPresent.ID} {
		require.NoError(t, f.c.dbClient.Ent().NarInfoNarFile.Create().
			SetNarinfoID(niID).
			SetNarFileID(nf.ID).
			Exec(f.ctx))
	}

	_, err = f.db.DB().ExecContext(f.ctx,
		"UPDATE nar_files SET created_at = ? WHERE hash = ?",
		time.Now().Add(-10*time.Minute), narHash)
	require.NoError(t, err)

	f.runRecovery(t)

	assert.True(t, f.narFileExists(t, narHash),
		"the placeholder must be kept when any linked narinfo is still present upstream")
}

// TestRecoveryGCDeletesOrphanedPlaceholder verifies that a backing-less nar_file with
// no linked narinfo at all (an orphan that can never be resolved or served) is
// garbage-collected outright rather than re-scanned every sweep.
func TestRecoveryGCDeletesOrphanedPlaceholder(t *testing.T) {
	t.Parallel()

	f := newGCTestFixture(t)

	const narHash = "6lid9xrpirkzcpqsxfq02qwiq0yd70ch"

	_, err := f.c.dbClient.Ent().NarFile.Create().
		SetHash(narHash).
		SetCompression(nar.CompressionTypeNone.String()).
		SetQuery("").
		SetFileSize(1234).
		Save(f.ctx)
	require.NoError(t, err)

	_, err = f.db.DB().ExecContext(f.ctx,
		"UPDATE nar_files SET created_at = ? WHERE hash = ?",
		time.Now().Add(-10*time.Minute), narHash)
	require.NoError(t, err)

	require.True(t, f.narFileExists(t, narHash))

	f.runRecovery(t)

	assert.False(t, f.narFileExists(t, narHash),
		"an orphan nar_file with no linked narinfo must be garbage-collected")
}
