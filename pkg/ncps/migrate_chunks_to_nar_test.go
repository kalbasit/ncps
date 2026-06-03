package ncps_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/nixhash"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	entnarinfo "github.com/kalbasit/ncps/ent/narinfo"

	"github.com/kalbasit/ncps/pkg/database"
	"github.com/kalbasit/ncps/pkg/ncps"
	"github.com/kalbasit/ncps/testdata"
	"github.com/kalbasit/ncps/testhelper"
)

// setupChunkedNar prepares a CDC-chunked NAR via the real CLI path
// (migrate-narinfo then migrate-nar-to-chunks) and returns the app + db URL.
// The narinfo's recorded NarHash is left as testdata's literal value, which does
// NOT match the random NarText — callers that exercise the success path must fix
// it up to the true content hash first.
func setupChunkedNar(
	ctx context.Context, t *testing.T, dbClient *database.Client, dir, dbURL string,
) *cli.Command {
	t.Helper()

	narInfoPath := filepath.Join(dir, "store", "narinfo", testdata.Nar1.NarInfoPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narInfoPath), 0o755))
	require.NoError(t, os.WriteFile(narInfoPath, []byte(testdata.Nar1.NarInfoText), 0o600))

	narPath := filepath.Join(dir, "store", "nar", testdata.Nar1.NarPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(narPath), 0o755))
	require.NoError(t, os.WriteFile(narPath, []byte(testdata.Nar1.NarText), 0o600))

	ni, err := narinfo.Parse(strings.NewReader(testdata.Nar1.NarInfoText))
	require.NoError(t, err)
	require.NoError(t, testhelper.RegisterNarInfoAsUnmigrated(ctx, dbClient, testdata.Nar1.NarInfoHash, ni))

	app, err := ncps.New()
	require.NoError(t, err)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-narinfo",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--concurrency", "1",
	}))

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-nar-to-chunks",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	var chunks int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&chunks))
	require.Positive(t, chunks, "precondition: NAR should be chunked")

	return app
}

func fixupNarHash(ctx context.Context, t *testing.T, dbClient *database.Client) {
	t.Helper()

	sum := sha256.Sum256([]byte(testdata.Nar1.NarText))
	narHash := nixhash.MustNewHashWithEncoding(nixhash.SHA256, sum[:], nixhash.NixBase32, true).String()

	_, err := dbClient.Ent().NarInfo.Update().
		Where(entnarinfo.HashEQ(testdata.Nar1.NarInfoHash)).
		SetNarHash(narHash).
		Save(ctx)
	require.NoError(t, err)
}

func countChunks(ctx context.Context, t *testing.T, dbClient *database.Client) int {
	t.Helper()

	var n int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks").Scan(&n))

	return n
}

// TestMigrateChunksToNar_CLI_NothingToMigrate verifies that the command exits cleanly
// when no chunked NARs exist — regardless of whether cdc_enabled is in the database.
// This covers both the "CDC never used" case and the "drain complete" case.
func TestMigrateChunksToNar_CLI_NothingToMigrate(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	_, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)

	// No configureCDCInDatabase call — cdc_enabled absent from DB.
	// No setupChunkedNar call — no nar_file rows with total_chunks > 0.

	app, err := ncps.New()
	require.NoError(t, err)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))
}

func TestMigrateChunksToNar_CLI_Success(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	var totalChunks int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx,
		"SELECT total_chunks FROM nar_files WHERE hash = ?", testdata.Nar1.NarHash).Scan(&totalChunks))
	assert.Zero(t, totalChunks, "nar_file should be flipped to whole-file")
	assert.Positive(t, countChunks(ctx, t, dbClient),
		"the default run leaves now-orphaned chunks for the GC (no --force-reclaim)")
}

func TestMigrateChunksToNar_CLI_ForceReclaim(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--force-reclaim",
	}))

	assert.Zero(t, countChunks(ctx, t, dbClient), "--force-reclaim must reclaim the now-orphaned chunks")
}

func TestMigrateChunksToNar_CLI_DryRunMakesNoChanges(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	before := countChunks(ctx, t, dbClient)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
		"--dry-run",
	}))

	assert.Equal(t, before, countChunks(ctx, t, dbClient), "--dry-run must not delete any chunks")
}

//nolint:paralleltest // redirects os.Stdout and overrides global ticker interval; cannot run in parallel
func TestMigrateChunksToNar_CLI_ProgressLogEmitted(t *testing.T) {
	orig := *ncps.MigrateChunksToNarProgressIntervalForTest
	*ncps.MigrateChunksToNarProgressIntervalForTest = 1 * time.Millisecond

	t.Cleanup(func() { *ncps.MigrateChunksToNarProgressIntervalForTest = orig })

	// Run setup before capturing stdout so only migrate-chunks-to-nar output is captured.
	ctx := context.Background()

	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	// The CLI's getZeroLogger writes to os.Stdout; capture it via a pipe.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})

	// Drain concurrently to prevent deadlock if output exceeds the OS pipe buffer.
	var logBuf bytes.Buffer

	readDone := make(chan struct{})

	go func() {
		_, _ = io.Copy(&logBuf, r)

		close(readDone)
	}()

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	require.NoError(t, w.Close())
	<-readDone

	logged := logBuf.String()
	assert.Contains(t, logged, "migration progress", "expected at least one progress log line")
	assert.Contains(t, logged, `"total"`, "progress log must include total field")
	assert.Contains(t, logged, `"processed"`, "progress log must include processed field")
	assert.Contains(t, logged, `"succeeded"`, "progress log must include succeeded field")
	assert.Contains(t, logged, `"failed"`, "progress log must include failed field")
	assert.Contains(t, logged, `"skipped"`, "progress log must include skipped field")
	assert.Contains(t, logged, `"purged"`, "progress log must include purged field")
	assert.Contains(t, logged, `"percent"`, "progress log must include percent field")
	assert.Contains(t, logged, `"elapsed"`, "progress log must include elapsed field")
	assert.Contains(t, logged, `"rate"`, "progress log must include rate field")
	assert.Less(
		t,
		strings.LastIndex(logged, "migration progress"),
		strings.Index(logged, "migration completed"),
		"all progress lines must appear before migration completed",
	)
}

//nolint:paralleltest // redirects os.Stdout; cannot run in parallel
func TestMigrateChunksToNar_CLI_NoProgressLogOnEmptyRun(t *testing.T) {
	// The CLI's getZeroLogger writes to os.Stdout; capture it via a pipe.
	oldStdout := os.Stdout
	r, w, pipeErr := os.Pipe()
	require.NoError(t, pipeErr)

	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})

	// Drain concurrently to prevent deadlock if output exceeds the OS pipe buffer.
	var logBuf bytes.Buffer

	readDone := make(chan struct{})

	go func() {
		_, _ = io.Copy(&logBuf, r)

		close(readDone)
	}()

	ctx := context.Background()

	_, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)

	app, err := ncps.New()
	require.NoError(t, err)

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}))

	require.NoError(t, w.Close())
	<-readDone

	assert.NotContains(t, logBuf.String(), "migration progress", "no progress line expected when no chunked NARs exist")
}

// TestMigrateChunksToNar_CLI_HashMismatchPurgesNarAndExitsZero verifies that a
// NAR whose reconstructed bytes do not match the recorded hash is purged (nar_file
// record + orphaned chunks deleted) and the command exits 0. The narinfo is left
// intact so the next GetNar triggers a fresh upstream fetch.
func TestMigrateChunksToNar_CLI_HashMismatchPurgesNarAndExitsZero(t *testing.T) {
	t.Parallel()

	ctx := zerolog.New(os.Stderr).WithContext(context.Background())
	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	// No hash fixup: testdata's literal NarHash does not match the content, so
	// reconstruction will always produce a hash mismatch.
	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)

	require.Positive(t, countChunks(ctx, t, dbClient), "precondition: chunks must exist")

	require.NoError(t, app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	}), "hash-mismatch NARs are purged; the command must exit 0")

	// nar_file record must be gone
	var totalChunks int

	err := dbClient.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM nar_files WHERE hash = ?", testdata.Nar1.NarHash).Scan(&totalChunks)
	require.NoError(t, err)
	assert.Zero(t, totalChunks, "nar_file record for the mismatched NAR must be deleted")

	// orphaned chunk objects must be reclaimed
	assert.Zero(t, countChunks(ctx, t, dbClient),
		"orphaned chunks for the purged NAR must be deleted")

	// narinfo must remain so the next GetNarInfo can trigger a re-fetch
	var narInfoCount int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM narinfos WHERE hash = ?", testdata.Nar1.NarInfoHash).Scan(&narInfoCount))
	assert.Positive(t, narInfoCount, "narinfo must be retained after purge")
}

// seedBrokenChunkedNar inserts a COMPLETED chunked NAR (total_chunks=2) that is
// missing a junction link (only 1 present) — the production
// nar_file_chunks.chunk_id -> chunks(id) ON DELETE CASCADE corruption — with a
// linked narinfo carrying a NarHash, so the reverse migration treats it as
// un-reassemblable (ErrMissingChunk) and purges it rather than reconstructing a
// truncated NAR. Uses testdata.Nar2's identifiers so it is distinct from the
// Nar1 fixture.
func seedBrokenChunkedNar(ctx context.Context, t *testing.T, dbClient *database.Client) {
	t.Helper()

	sum := sha256.Sum256([]byte(testdata.Nar2.NarText))
	narHash := nixhash.MustNewHashWithEncoding(nixhash.SHA256, sum[:], nixhash.NixBase32, true).String()

	ni, err := dbClient.Ent().NarInfo.Create().
		SetHash(testdata.Nar2.NarInfoHash).
		SetURL("nar/" + testdata.Nar2.NarHash + ".nar").
		SetNarHash(narHash).
		Save(ctx)
	require.NoError(t, err)

	nf, err := dbClient.Ent().NarFile.Create().
		SetHash(testdata.Nar2.NarHash).
		SetCompression("none").
		SetQuery("").
		SetFileSize(12345).
		SetTotalChunks(2).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarInfoNarFile.Create().
		SetNarinfoID(ni.ID).
		SetNarFileID(nf.ID).
		Save(ctx)
	require.NoError(t, err)

	// Only ONE of the two declared chunk links — the gap that makes the NAR
	// un-reassemblable. No physical blob is needed: the completeness guard fires
	// on the link-count mismatch before any chunk is read.
	ch, err := dbClient.Ent().Chunk.Create().
		SetHash(testhelper.MustRandBase32NarHash()).
		SetSize(64).
		SetCompressedSize(64).
		Save(ctx)
	require.NoError(t, err)

	_, err = dbClient.Ent().NarFileChunk.Create().
		SetNarFileID(nf.ID).
		SetChunkID(ch.ID).
		SetChunkIndex(0).
		Save(ctx)
	require.NoError(t, err)
}

// TestMigrateChunksToNar_CLI_SkipsBrokenNarMigratesRestAndReports verifies the
// drain-hardening contract end to end: with a mix of one reassemblable NAR (Nar1,
// hash fixed up) and one un-reassemblable NAR (Nar2, missing a junction link), the
// migrate command migrates the good one to a whole file, purges the broken one,
// continues past the failure (exit 0), and reports the outcome — including the
// purged NAR by hash — in its summary.
//
//nolint:paralleltest // redirects os.Stdout; cannot run in parallel
func TestMigrateChunksToNar_CLI_SkipsBrokenNarMigratesRestAndReports(t *testing.T) {
	ctx := context.Background()

	dbClient, _, dir, dbURL, cleanup := setupNarToChunksMigrationSQLite(t)
	t.Cleanup(cleanup)
	configureCDCInDatabase(ctx, t, dbClient)

	// Good NAR (Nar1): valid recorded hash → reconstructs, verifies, migrates.
	app := setupChunkedNar(ctx, t, dbClient, dir, dbURL)
	fixupNarHash(ctx, t, dbClient)

	// Broken NAR (Nar2): completed chunked record missing a junction link → purged.
	seedBrokenChunkedNar(ctx, t, dbClient)

	// Capture the command's stdout logger (it writes JSON lines to os.Stdout).
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	t.Cleanup(func() {
		os.Stdout = oldStdout
		_ = r.Close()
	})

	var logBuf bytes.Buffer

	readDone := make(chan struct{})

	go func() {
		_, _ = io.Copy(&logBuf, r)

		close(readDone)
	}()

	runErr := app.Run(ctx, []string{
		"ncps", "migrate-chunks-to-nar",
		"--cache-database-url", dbURL,
		"--cache-storage-local", dir,
	})

	require.NoError(t, w.Close())
	<-readDone

	require.NoError(t, runErr, "a broken NAR must be skipped/purged, not abort the run (exit 0)")

	// The reassemblable NAR migrated: its record is flipped to whole-file.
	var nar1Chunks int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx,
		"SELECT total_chunks FROM nar_files WHERE hash = ?", testdata.Nar1.NarHash).Scan(&nar1Chunks))
	assert.Zero(t, nar1Chunks, "the reassemblable NAR must still be migrated to a whole file")

	// The un-reassemblable NAR was purged so its hash re-fetches from upstream later.
	var nar2Rows int
	require.NoError(t, dbClient.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM nar_files WHERE hash = ?", testdata.Nar2.NarHash).Scan(&nar2Rows))
	assert.Zero(t, nar2Rows, "the un-reassemblable NAR's nar_file record must be purged")

	// The run reports the outcome, including the purged NAR by hash.
	logged := logBuf.String()
	assert.Contains(t, logged, "migration completed", "must emit a final summary")
	assert.Contains(t, logged, `"succeeded":1`, "summary must report one migrated NAR")
	assert.Contains(t, logged, `"purged":1`, "summary must report one purged NAR")
	assert.Contains(t, logged, testdata.Nar2.NarHash, "the purged NAR must be reported by hash")
}
